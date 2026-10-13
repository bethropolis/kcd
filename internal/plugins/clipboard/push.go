package clipboard

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

// Push copies the local clipboard to the remote device using wl-paste or xclip -o.
func Push(ctx context.Context, dev device.Sender, p *ClipboardPlugin) error {
	var cmd *exec.Cmd
	switch backend, _ := p.getBackend(); backend {
	case backendWayland:
		cmd = p.clipboardCmd("wl-paste", "-n")
	case backendX11:
		cmd = p.clipboardCmd("xclip", "-selection", "clipboard", "-o")
	default:
		return fmt.Errorf("clipboard: no clipboard tool available")
	}

	out, err := p.runClipboard(ctx, cmd)
	if err != nil {
		// Empty clipboard is not a push failure: the tool exits non-zero with
		// "no selection", which would otherwise spam errors until first copy.
		if isNoSelection(err) {
			return nil
		}
		return err
	}

	content := string(out)
	if content == "" {
		return nil
	}

	p.mu.Lock()
	// Skip content matching the phone's last or our last push, compared
	// newline-normalized: otherwise the phone's content echoes back to it.
	if normClip(content) == normClip(p.lastContent) || normClip(content) == normClip(p.lastPushedContent) {
		p.mu.Unlock()
		return nil
	}
	p.lastPushedContent = content
	p.mu.Unlock()

	pkt, err := protocol.NewPacket("kdeconnect.clipboard", ClipboardBody{
		Content: content,
	})
	if err != nil {
		return err
	}

	// All outgoing packets must use device.Send.
	return dev.Send(pkt)
}

// normClip strips trailing newlines so read-back content compares equal
// regardless of which tool appended one.
func normClip(s string) string {
	return strings.TrimRight(s, "\n")
}

func (p *ClipboardPlugin) readClipboard() string {
	var cmd *exec.Cmd
	switch backend, _ := p.getBackend(); backend {
	case backendWayland:
		cmd = p.clipboardCmd("wl-paste", "-n")
	case backendX11:
		cmd = p.clipboardCmd("xclip", "-selection", "clipboard", "-o")
	default:
		return ""
	}
	out, err := p.runClipboard(context.Background(), cmd)
	if err != nil {
		p.logger.Debug("clipboard: read failed", log.Error(err))
		return ""
	}
	return string(out)
}

func (p *ClipboardPlugin) OnConnect(dev device.Sender) {
	if !p.pushOnConnect {
		return
	}
	content := p.readClipboard()

	p.mu.Lock()
	p.lastPushedContent = content
	p.mu.Unlock()

	body := ClipboardBody{
		Content:   content,
		Timestamp: time.Now().UnixMilli(),
	}
	pkt, err := protocol.NewPacket("kdeconnect.clipboard.connect", body)
	if err != nil {
		p.logger.Debug("clipboard: OnConnect: failed to build packet", log.Error(err))
		return
	}
	// Best-effort — device may still be completing the TLS handshake.
	_ = dev.Send(pkt)
}

func (p *ClipboardPlugin) OnDisconnect(dev device.Sender) {
}

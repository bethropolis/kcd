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
		// An empty clipboard (fresh session, nothing copied yet) is not an
		// error worth failing a push for — the tool exits non-zero with a
		// "no selection"-style message. Treat it as nothing to push so the
		// CLI and --watch don't spam errors until something is copied.
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
	// Skip if content matches what we last received from the phone (lastContent)
	// OR what we last pushed outbound (lastPushedContent).
	//
	// lastContent guard: prevents sending the phone's own content back.
	// lastPushedContent guard: prevents duplicate pushes when the local
	// clipboard hasn't changed between two Push calls.
	//
	// Comparisons are normalized of trailing newlines so a stray \n appended
	// by some clipboard tooling cannot silently disable the guard and cause
	// an echo of the phone's own content back to it.
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

// normClip strips trailing newlines so content read back from wl-copy/xclip
// compares equal to the raw content we stored, regardless of which tool
// appended (or not) a trailing newline.
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

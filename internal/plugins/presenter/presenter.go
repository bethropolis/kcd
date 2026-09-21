// Package presenter implements the KDE Connect presenter remote plugin.
// It receives gyroscope-based pointer movements from the phone and moves
// the system cursor accordingly. Special keys (next/prev/fullscreen/esc)
// are handled by the mousepad plugin via kdeconnect.mousepad.request.
package presenter

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
)

// PresenterPlugin handles kdeconnect.presenter packets containing
// gyroscope-based pointer deltas (dx/dy) from the Android presenter remote.
type PresenterPlugin struct {
	logger log.Logger

	mu      sync.Mutex
	xPos    float64
	yPos    float64
	ratio   float64
	screenW int
	screenH int

	moveCh chan PresenterBody
}

// PresenterBody is the payload of a kdeconnect.presenter packet.
type PresenterBody struct {
	Dx   *float64 `json:"dx,omitempty"`
	Dy   *float64 `json:"dy,omitempty"`
	Stop *bool    `json:"stop,omitempty"`
}

// NewPresenterPlugin creates a new presenter remote plugin.
func NewPresenterPlugin(logger log.Logger) *PresenterPlugin {
	p := &PresenterPlugin{
		logger: logger.With(log.String("plugin", "Presenter")),
		xPos:   0.5,
		yPos:   0.5,
		moveCh: make(chan PresenterBody, 1),
	}
	p.detectScreen()
	go p.worker()
	return p
}

func (p *PresenterPlugin) detectScreen() {
	w, h := getScreenSize()
	if w > 0 && h > 0 {
		p.screenW = w
		p.screenH = h
		p.ratio = float64(w) / float64(h)
	} else {
		p.screenW = 1920
		p.screenH = 1080
		p.ratio = 1920.0 / 1080.0
	}
}

func (p *PresenterPlugin) Name() string            { return "Presenter" }
func (p *PresenterPlugin) Timeout() time.Duration  { return 2 * time.Second }
func (p *PresenterPlugin) IncomingTypes() []string { return []string{"kdeconnect.presenter"} }
func (p *PresenterPlugin) OutgoingTypes() []string { return nil }

func (p *PresenterPlugin) OnConnect(_ device.Sender)    {}
func (p *PresenterPlugin) OnDisconnect(_ device.Sender) {}

// Handle dispatches a presenter packet to the worker goroutine.
// It returns immediately as required by the plugin contract.
func (p *PresenterPlugin) Handle(_ context.Context, _ device.Sender, pkt *protocol.Packet) error {
	var body PresenterBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	select {
	case p.moveCh <- body:
	default:
		select {
		case <-p.moveCh:
		default:
		}
		p.moveCh <- body
	}
	return nil
}

func (p *PresenterPlugin) worker() {
	for body := range p.moveCh {
		p.handleBody(body)
	}
}

func (p *PresenterPlugin) handleBody(body PresenterBody) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if body.Stop != nil && *body.Stop {
		p.xPos = 0.5
		p.yPos = 0.5
		return
	}

	if body.Dx != nil {
		p.xPos += *body.Dx
	}
	if body.Dy != nil {
		p.yPos += *body.Dy * p.ratio
	}
	if p.xPos < 0 {
		p.xPos = 0
	}
	if p.xPos > 1 {
		p.xPos = 1
	}
	if p.yPos < 0 {
		p.yPos = 0
	}
	if p.yPos > 1 {
		p.yPos = 1
	}

	x := int(p.xPos * float64(p.screenW))
	y := int(p.yPos * float64(p.screenH))
	moveCursor(x, y)
}

// getScreenSize attempts to detect the screen resolution via xdpyinfo (X11)
// or wlr-randr (Wayland). Returns 0x0 if neither is available.
func getScreenSize() (int, int) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := plugin.RunCommandOutput(ctx, "xdpyinfo")
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "dimensions:") {
				var w, h int
				if _, err := fmt.Sscanf(line, "  dimensions: %dx%d pixels", &w, &h); err == nil {
					return w, h
				}
			}
		}
	}

	out, err = plugin.RunCommandOutput(ctx, "wlr-randr")
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.Contains(line, "x") && strings.HasSuffix(line, " px") {
				var w, h int
				if _, err := fmt.Sscanf(line, "%dx%d px", &w, &h); err == nil {
					return w, h
				}
			}
		}
	}

	return 0, 0
}

// moveCursor moves the system cursor to the given absolute screen coordinates.
// Prefers ydotool (Wayland) with xdotool as fallback (X11).
func moveCursor(x, y int) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := plugin.RunCommandSync(ctx, "ydotool", "mousemove", "-x", strconv.Itoa(x), "-y", strconv.Itoa(y)); err == nil {
		return
	}

	_, _ = plugin.RunCommandSync(ctx, "xdotool", "mousemove", strconv.Itoa(x), strconv.Itoa(y))
}

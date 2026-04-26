package mousepad

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

type MousepadPlugin struct {
	logger    *zap.Logger
	isWayland bool
	moveCh    chan MousepadBody // capacity 1, older frames dropped
	eventCh   chan MousepadBody // capacity 64, clicks + keys
}

func NewMousepadPlugin(logger *zap.Logger) *MousepadPlugin {
	p := &MousepadPlugin{
		logger:    logger.With(zap.String("plugin", "mousepad")),
		isWayland: os.Getenv("WAYLAND_DISPLAY") != "",
		moveCh:    make(chan MousepadBody, 1),
		eventCh:   make(chan MousepadBody, 64),
	}
	go p.worker()
	return p
}

type MousepadBody struct {
	Dx          float64 `json:"dx"`
	Dy          float64 `json:"dy"`
	SingleClick bool    `json:"singleclick"`
	RightClick  bool    `json:"rightclick"`
	MiddleClick bool    `json:"middleclick"`
	Scroll      bool    `json:"scroll"`
	Key         string  `json:"key"`
	SpecialKey  int     `json:"specialKey"`
}

func (p *MousepadPlugin) Name() string            { return "Mousepad" }
func (p *MousepadPlugin) Timeout() time.Duration  { return 2 * time.Second }
func (p *MousepadPlugin) IncomingTypes() []string { return []string{"kdeconnect.mousepad.request"} }
func (p *MousepadPlugin) OutgoingTypes() []string { return []string{} }

func (p *MousepadPlugin) Handle(_ context.Context, _ device.Sender, pkt *protocol.Packet) error {
	var body MousepadBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	isPointerMove := (body.Dx != 0 || body.Dy != 0) && !body.SingleClick &&
		!body.RightClick && !body.MiddleClick && body.Key == "" && body.SpecialKey == 0

	if isPointerMove {
		// Drop stale frame if worker hasn't consumed the last one yet.
		select {
		case p.moveCh <- body:
		default:
			select {
			case <-p.moveCh: // drain
			default:
			}
			p.moveCh <- body
		}
	} else {
		p.eventCh <- body
	}
	return nil
}

// worker is the single persistent goroutine that processes all mousepad events.
func (p *MousepadPlugin) worker() {
	for {
		select {
		case body := <-p.moveCh:
			p.handleMove(body)
		case body := <-p.eventCh:
			p.handleEvent(body)
		}
	}
}

// handleMove handles pointer movement and scroll events.
func (p *MousepadPlugin) handleMove(body MousepadBody) {
	if body.Dx == 0 && body.Dy == 0 {
		return
	}
	if body.Scroll {
		if p.isWayland {
			if body.Dy > 0 {
				p.runCmd("ydotool", "mousescroll", "--", "0", "1")
			} else if body.Dy < 0 {
				p.runCmd("ydotool", "mousescroll", "--", "0", "-1")
			}
		} else {
			if body.Dy > 0 {
				p.runCmd("xdotool", "click", "5")
			} else if body.Dy < 0 {
				p.runCmd("xdotool", "click", "4")
			}
		}
	} else {
		if p.isWayland {
			dx := strconv.FormatFloat(body.Dx, 'f', 0, 64)
			dy := strconv.FormatFloat(body.Dy, 'f', 0, 64)
			p.runCmd("ydotool", "mousemove", "-x", dx, "-y", dy)
		} else {
			dx := strconv.FormatFloat(body.Dx, 'f', 0, 64)
			dy := strconv.FormatFloat(body.Dy, 'f', 0, 64)
			p.runCmd("xdotool", "mousemove_relative", "--", dx, dy)
		}
	}
}

// handleEvent handles clicks and key events.
func (p *MousepadPlugin) handleEvent(body MousepadBody) {
	if body.SingleClick {
		if p.isWayland {
			p.runCmd("ydotool", "click", "0xC0")
		} else {
			p.runCmd("xdotool", "click", "1")
		}
	}
	if body.RightClick {
		if p.isWayland {
			p.runCmd("ydotool", "click", "0xC1")
		} else {
			p.runCmd("xdotool", "click", "3")
		}
	}
	if body.MiddleClick {
		if p.isWayland {
			p.runCmd("ydotool", "click", "0xC2")
		} else {
			p.runCmd("xdotool", "click", "2")
		}
	}
	if body.Key != "" {
		if p.isWayland {
			p.runCmd("wtype", body.Key)
		} else {
			p.runCmd("xdotool", "type", "--", body.Key)
		}
	}
	if body.SpecialKey != 0 {
		keyName := mapSpecialKey(body.SpecialKey)
		if keyName != "" {
			if p.isWayland {
				p.runCmd("wtype", "-k", keyName)
			} else {
				p.runCmd("xdotool", "key", keyName)
			}
		}
	}
}

func (p *MousepadPlugin) runCmd(name string, arg ...string) {
	if out, err := exec.Command(name, arg...).CombinedOutput(); err != nil {
		p.logger.Debug("command failed", zap.String("cmd", name), zap.Error(err), zap.String("output", string(out)))
	}
}

func mapSpecialKey(k int) string {
	switch k {
	case 1:
		return "BackSpace"
	case 2:
		return "Tab"
	case 3, 12:
		return "Return"
	case 4:
		return "Left"
	case 5:
		return "Up"
	case 6:
		return "Right"
	case 7:
		return "Down"
	case 8:
		return "Prior"
	case 9:
		return "Next"
	case 10:
		return "Home"
	case 11:
		return "End"
	case 13:
		return "Delete"
	case 14:
		return "Escape"
	case 16:
		return "Scroll_Lock"
	case 21:
		return "F1"
	case 22:
		return "F2"
	case 23:
		return "F3"
	case 24:
		return "F4"
	case 25:
		return "F5"
	case 26:
		return "F6"
	case 27:
		return "F7"
	case 28:
		return "F8"
	case 29:
		return "F9"
	case 30:
		return "F10"
	case 31:
		return "F11"
	case 32:
		return "F12"
	default:
		return ""
	}
}

func (p *MousepadPlugin) OnConnect(_ device.Sender)    {}
func (p *MousepadPlugin) OnDisconnect(_ device.Sender) {}

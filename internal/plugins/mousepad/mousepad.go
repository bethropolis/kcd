package mousepad

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/bendahl/uinput"
	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

type MousepadPlugin struct {
	logger     *zap.Logger
	cfg        config.MousepadConfig
	useYdotool bool
	useUinput  bool

	// uinput devices
	mouse    uinput.Mouse
	keyboard uinput.Keyboard

	moveCh  chan MousepadBody // capacity 1, older frames dropped
	eventCh chan MousepadBody // capacity 64, clicks + keys
}

func NewMousepadPlugin(cfg config.MousepadConfig, logger *zap.Logger) *MousepadPlugin {
	p := &MousepadPlugin{
		logger:  logger.With(zap.String("plugin", "mousepad")),
		cfg:     cfg,
		moveCh:  make(chan MousepadBody, 1),
		eventCh: make(chan MousepadBody, 64),
	}

	// Try uinput first if auto or explicit
	if cfg.Backend == "auto" || cfg.Backend == "uinput" {
		if err := p.initUinput(); err != nil {
			p.logger.Warn("uinput initialization failed, falling back to legacy backends", zap.Error(err))
		} else {
			p.useUinput = true
			p.logger.Info("uinput initialized successfully")
		}
	}

	if !p.useUinput {
		switch cfg.Backend {
		case "ydotool":
			p.useYdotool = true
		case "xdotool":
			p.useYdotool = false
		default: // auto
			p.useYdotool = os.Getenv("WAYLAND_DISPLAY") != ""
		}
	}

	go p.worker()
	return p
}

func (p *MousepadPlugin) initUinput() error {
	m, err := uinput.CreateMouse("/dev/uinput", []byte("kcd-mouse"))
	if err != nil {
		return fmt.Errorf("create mouse: %w", err)
	}
	p.mouse = m

	k, err := uinput.CreateKeyboard("/dev/uinput", []byte("kcd-keyboard"))
	if err != nil {
		m.Close()
		return fmt.Errorf("create keyboard: %w", err)
	}
	p.keyboard = k

	return nil
}

// MousepadBody represents the exact spec Android sends.
type MousepadBody struct {
	Dx          float64 `json:"dx"`
	Dy          float64 `json:"dy"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	SingleClick bool    `json:"singleclick"`
	DoubleClick bool    `json:"doubleclick"`
	MiddleClick bool    `json:"middleclick"`
	RightClick  bool    `json:"rightclick"`
	SingleHold  bool    `json:"singlehold"`
	SingleRel   bool    `json:"singlerelease"`
	Scroll      bool    `json:"scroll"`
	Key         string  `json:"key"`
	SpecialKey  int     `json:"specialKey"`
	Shift       bool    `json:"shift"`
	Ctrl        bool    `json:"ctrl"`
	Alt         bool    `json:"alt"`
	Super       bool    `json:"super"`
}

func (p *MousepadPlugin) Name() string            { return "Mousepad" }
func (p *MousepadPlugin) Timeout() time.Duration  { return 2 * time.Second }
func (p *MousepadPlugin) IncomingTypes() []string { return []string{"kdeconnect.mousepad.request"} }
func (p *MousepadPlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.mousepad.keyboardstate"}
}

func (p *MousepadPlugin) Handle(_ context.Context, _ device.Sender, pkt *protocol.Packet) error {
	var body MousepadBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	isPointerMove := (body.Dx != 0 || body.Dy != 0) && !body.SingleClick && !body.DoubleClick &&
		!body.RightClick && !body.MiddleClick && !body.SingleHold && !body.SingleRel &&
		body.Key == "" && body.SpecialKey == 0

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

func (p *MousepadPlugin) handleMove(body MousepadBody) {
	if body.Dx == 0 && body.Dy == 0 {
		return
	}
	if body.Scroll {
		if p.useUinput {
			if body.Dy > 0 {
				p.mouse.Wheel(false, -1)
			} else if body.Dy < 0 {
				p.mouse.Wheel(false, 1)
			}
		} else if p.useYdotool {
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
		if p.useUinput {
			p.mouse.Move(int32(body.Dx), int32(body.Dy))
		} else if p.useYdotool {
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

func (p *MousepadPlugin) handleEvent(body MousepadBody) {
	// 1. Mouse Actions
	if body.SingleClick {
		if p.useUinput {
			p.mouse.LeftClick()
		} else if p.useYdotool {
			p.runCmd("ydotool", "click", "0xC0")
		} else {
			p.runCmd("xdotool", "click", "1")
		}
	}
	if body.DoubleClick {
		if p.useUinput {
			p.mouse.LeftClick()
			p.mouse.LeftClick()
		} else if p.useYdotool {
			p.runCmd("ydotool", "click", "0xC0", "0xC0")
		} else {
			p.runCmd("xdotool", "click", "--repeat", "2", "1")
		}
	}
	if body.RightClick {
		if p.useUinput {
			p.mouse.RightClick()
		} else if p.useYdotool {
			p.runCmd("ydotool", "click", "0xC1")
		} else {
			p.runCmd("xdotool", "click", "3")
		}
	}
	if body.MiddleClick {
		if p.useUinput {
			p.mouse.MiddleClick()
		} else if p.useYdotool {
			p.runCmd("ydotool", "click", "0xC2")
		} else {
			p.runCmd("xdotool", "click", "2")
		}
	}
	if body.SingleHold {
		if p.useUinput {
			p.mouse.LeftPress()
		} else if p.useYdotool {
			p.runCmd("ydotool", "click", "0x40")
		} else {
			p.runCmd("xdotool", "mousedown", "1")
		}
	}
	if body.SingleRel {
		if p.useUinput {
			p.mouse.LeftRelease()
		} else if p.useYdotool {
			p.runCmd("ydotool", "click", "0x80")
		} else {
			p.runCmd("xdotool", "mouseup", "1")
		}
	}

	// 2. Modifiers
	if p.useUinput {
		if body.Ctrl {
			p.keyboard.KeyDown(uinput.KeyLeftctrl)
		}
		if body.Alt {
			p.keyboard.KeyDown(uinput.KeyLeftalt)
		}
		if body.Shift {
			p.keyboard.KeyDown(uinput.KeyLeftshift)
		}
		if body.Super {
			p.keyboard.KeyDown(uinput.KeyLeftmeta)
		}
	}

	// 3. Keys
	if body.SpecialKey != 0 {
		if p.useUinput {
			ukey := mapUinputKey(body.SpecialKey)
			if ukey != -1 {
				p.keyboard.KeyPress(ukey)
			} else {
				keyName := mapSpecialKey(body.SpecialKey)
				if keyName != "" {
					p.execKeyFallback(keyName)
				}
			}
		} else {
			keyName := mapSpecialKey(body.SpecialKey)
			if keyName != "" {
				p.execKeyFallback(keyName)
			}
		}
	} else if body.Key != "" {
		// Unicode strings cannot be typed via uinput directly.
		// We fallback to tools that interface with the display server (X11/Wayland).
		if p.useYdotool {
			p.runCmd("wtype", body.Key)
		} else {
			p.runCmd("xdotool", "type", "--", body.Key)
		}
	}

	// 4. Release Modifiers
	if p.useUinput {
		if body.Super {
			p.keyboard.KeyUp(uinput.KeyLeftmeta)
		}
		if body.Shift {
			p.keyboard.KeyUp(uinput.KeyLeftshift)
		}
		if body.Alt {
			p.keyboard.KeyUp(uinput.KeyLeftalt)
		}
		if body.Ctrl {
			p.keyboard.KeyUp(uinput.KeyLeftctrl)
		}
	}
}

func (p *MousepadPlugin) execKeyFallback(keyName string) {
	if p.useYdotool {
		p.runCmd("wtype", "-k", keyName)
	} else {
		p.runCmd("xdotool", "key", keyName)
	}
}

func (p *MousepadPlugin) runCmd(name string, arg ...string) {
	if out, err := exec.Command(name, arg...).CombinedOutput(); err != nil {
		p.logger.Debug("command failed", zap.String("cmd", name), zap.Error(err), zap.String("output", string(out)))
	}
}

// OnConnect explicitly tells the Android app that this device supports Keyboard input.
// Without this, the Android app will not show the keyboard icon in the Remote Input UI.
func (p *MousepadPlugin) OnConnect(dev device.Sender) {
	pkt, _ := protocol.NewPacket("kdeconnect.mousepad.keyboardstate", map[string]bool{"state": true})
	dev.Send(pkt)
}

func (p *MousepadPlugin) OnDisconnect(_ device.Sender) {}

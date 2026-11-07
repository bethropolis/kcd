package mousepad

import (
	"context"
	"os"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/sysexec"
)

var _ plugin.Plugin = (*MousepadPlugin)(nil)

func NewMousepadPlugin(cfg config.MousepadConfig, logger log.Logger, executors ...*sysexec.Exec) *MousepadPlugin {
	ctx, cancel := context.WithCancel(context.Background())
	isWayland := os.Getenv("WAYLAND_DISPLAY") != ""
	exec := sysexec.New(nil)
	if len(executors) > 0 && executors[0] != nil {
		exec = executors[0]
	}
	p := &MousepadPlugin{
		logger:    logger.With(log.String("plugin", "mousepad")),
		exec:      exec,
		cfg:       cfg,
		isWayland: isWayland,
		queueWake: make(chan struct{}, 1),
		ctx:       ctx,
		cancel:    cancel,
	}

	// Try uinput first if auto or explicit
	if cfg.Backend == "auto" || cfg.Backend == "uinput" {
		if err := p.initUinput(); err != nil {
			p.logger.Warn("uinput initialization failed, falling back to legacy backends", log.Error(err))
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

func (p *MousepadPlugin) Name() string            { return "Mousepad" }
func (p *MousepadPlugin) Timeout() time.Duration  { return 2 * time.Second }
func (p *MousepadPlugin) IncomingTypes() []string { return []string{"kdeconnect.mousepad.request"} }
func (p *MousepadPlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.mousepad.keyboardstate"}
}

// OnConnect explicitly tells the Android app that this device supports Keyboard input.
// Without this, the Android app will not show the keyboard icon in the Remote Input UI.
func (p *MousepadPlugin) OnConnect(dev device.Peer) {
	pkt, _ := protocol.NewPacket("kdeconnect.mousepad.keyboardstate", map[string]bool{"state": true})
	_ = dev.Send(pkt)
}

func (p *MousepadPlugin) OnDisconnect(_ device.Peer) {}

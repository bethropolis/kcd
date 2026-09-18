package mousepad

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/bendahl/uinput"
	"github.com/bethropolis/kcd/internal/config"
	"go.uber.org/zap"
)

type MousepadPlugin struct {
	logger     *zap.Logger
	cfg        config.MousepadConfig
	useYdotool bool
	useUinput  bool
	isWayland  bool

	// uinput devices
	mouse    uinput.Mouse
	keyboard uinput.Keyboard

	moveCh  chan MousepadBody // capacity 1, older frames dropped
	eventCh chan MousepadBody // capacity 64, clicks + keys

	ctx    context.Context
	cancel context.CancelFunc
}

func NewMousepadPlugin(cfg config.MousepadConfig, logger *zap.Logger) *MousepadPlugin {
	ctx, cancel := context.WithCancel(context.Background())
	isWayland := os.Getenv("WAYLAND_DISPLAY") != ""
	p := &MousepadPlugin{
		logger:    logger.With(zap.String("plugin", "mousepad")),
		cfg:       cfg,
		isWayland: isWayland,
		moveCh:    make(chan MousepadBody, 1),
		eventCh:   make(chan MousepadBody, 64),
		ctx:       ctx,
		cancel:    cancel,
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

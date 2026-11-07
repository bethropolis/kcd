package mousepad

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/bendahl/uinput"
	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/sysexec"
)

type MousepadPlugin struct {
	logger     log.Logger
	exec       *sysexec.Exec
	cfg        config.MousepadConfig
	useYdotool bool
	useUinput  bool
	isWayland  bool

	xdotoolMu sync.Mutex
	xdotoolIn io.WriteCloser

	ydotoolMu   sync.Mutex
	ydotoolConn net.Conn

	// uinput devices
	mouse    uinput.Mouse
	keyboard uinput.Keyboard

	queueMu   sync.Mutex
	queue     []MousepadBody
	queueWake chan struct{}
	moveX     float64
	moveY     float64

	ctx    context.Context
	cancel context.CancelFunc
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

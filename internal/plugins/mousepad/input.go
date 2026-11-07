package mousepad

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bendahl/uinput"
	"github.com/bethropolis/kcd/internal/log"
)

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
				p.execYdotool("mousescroll", "--", "0", "1")
			} else if body.Dy < 0 {
				p.execYdotool("mousescroll", "--", "0", "-1")
			}
		} else {
			if body.Dy > 0 {
				p.execXdotool("click", "5")
			} else if body.Dy < 0 {
				p.execXdotool("click", "4")
			}
		}
	} else {
		if p.useUinput {
			p.mouse.Move(int32(body.Dx), int32(body.Dy))
		} else if p.useYdotool {
			p.handleYdotoolMove(body.Dx, body.Dy)
		} else {
			dx := strconv.FormatFloat(body.Dx, 'f', 0, 64)
			dy := strconv.FormatFloat(body.Dy, 'f', 0, 64)
			p.execXdotool("mousemove_relative", "--", dx, dy)
		}
	}
}

func (p *MousepadPlugin) handleEvent(body MousepadBody) {
	// 1. Mouse Actions
	if body.SingleClick {
		if p.useUinput {
			p.mouse.LeftClick()
		} else if p.useYdotool {
			p.execYdotool("click", "0xC0")
		} else {
			p.execXdotool("click", "1")
		}
	}
	if body.DoubleClick {
		if p.useUinput {
			p.mouse.LeftClick()
			p.mouse.LeftClick()
		} else if p.useYdotool {
			p.execYdotool("click", "0xC0", "0xC0")
		} else {
			p.execXdotool("click", "--repeat", "2", "1")
		}
	}
	if body.RightClick {
		if p.useUinput {
			p.mouse.RightClick()
		} else if p.useYdotool {
			p.execYdotool("click", "0xC1")
		} else {
			p.execXdotool("click", "3")
		}
	}
	if body.MiddleClick {
		if p.useUinput {
			p.mouse.MiddleClick()
		} else if p.useYdotool {
			p.execYdotool("click", "0xC2")
		} else {
			p.execXdotool("click", "2")
		}
	}
	if body.SingleHold {
		if p.useUinput {
			p.mouse.LeftPress()
		} else if p.useYdotool {
			p.execYdotool("click", "0x40")
		} else {
			p.execXdotool("mousedown", "1")
		}
	}
	if body.SingleRel {
		if p.useUinput {
			p.mouse.LeftRelease()
		} else if p.useYdotool {
			p.execYdotool("click", "0x80")
		} else {
			p.execXdotool("mouseup", "1")
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
					p.execKeyFallback(keyName, body)
				}
			}
		} else {
			keyName := mapSpecialKey(body.SpecialKey)
			if keyName != "" {
				p.execKeyFallback(keyName, body)
			}
		}
	} else if body.Key != "" {
		// Unicode can't go through uinput: use the display-server tool, with
		// "--" so leading '-' in phone text isn't misparsed as a flag.
		if p.isWayland {
			args := modifierArgs(body)
			args = append(args, body.Key)
			args = append(args, modifierReleaseArgs(body)...)
			p.runCmd("wtype", args...)
		} else {
			if key := modifiedKey(body, body.Key); key != "" {
				p.execXdotool("key", key)
			} else {
				p.execXdotool("type", "--", body.Key)
			}
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

func (p *MousepadPlugin) execKeyFallback(keyName string, body MousepadBody) {
	if p.isWayland {
		args := modifierArgs(body)
		args = append(args, "-k", keyName)
		args = append(args, modifierReleaseArgs(body)...)
		p.runCmd("wtype", args...)
	} else {
		key := keyName
		if mk := modifiedKey(body, keyName); mk != "" {
			key = mk
		}
		p.execXdotool("key", key)
	}
}

func modifierArgs(body MousepadBody) []string {
	var args []string
	for _, modifier := range modifierNames(body) {
		args = append(args, "-M", modifier)
	}
	return args
}

func modifierReleaseArgs(body MousepadBody) []string {
	mods := modifierNames(body)
	args := make([]string, 0, len(mods)*2)
	for i := len(mods) - 1; i >= 0; i-- {
		args = append(args, "-m", mods[i])
	}
	return args
}

func modifierNames(body MousepadBody) []string {
	var mods []string
	if body.Ctrl {
		mods = append(mods, "ctrl")
	}
	if body.Alt {
		mods = append(mods, "alt")
	}
	if body.Shift {
		mods = append(mods, "shift")
	}
	if body.Super {
		mods = append(mods, "super")
	}
	return mods
}

func modifiedKey(body MousepadBody, key string) string {
	mods := modifierNames(body)
	if len(mods) == 0 {
		return ""
	}
	for _, modifier := range mods {
		key = modifier + "+" + key
	}
	return key
}

func (p *MousepadPlugin) execXdotool(args ...string) {
	if err := p.sendXdotoolStream(args...); err == nil {
		return
	}
	p.runCmd("xdotool", args...)
}

func (p *MousepadPlugin) sendXdotoolStream(args ...string) error {
	p.xdotoolMu.Lock()
	defer p.xdotoolMu.Unlock()

	if p.xdotoolIn == nil {
		stream, err := p.exec.StartStream(p.ctx, "xdotool", "-")
		if err != nil {
			return err
		}
		p.xdotoolIn = stream
	}

	line := strings.Join(args, " ") + "\n"
	if _, err := io.WriteString(p.xdotoolIn, line); err != nil {
		_ = p.xdotoolIn.Close()
		p.xdotoolIn = nil
		return err
	}
	return nil
}

func (p *MousepadPlugin) execYdotool(args ...string) {
	p.runCmd("ydotool", args...)
}

func (p *MousepadPlugin) handleYdotoolMove(dx, dy float64) {
	if err := p.sendYdotoolSocketMove(int32(dx), int32(dy)); err == nil {
		return
	}
	dxStr := strconv.FormatFloat(dx, 'f', 0, 64)
	dyStr := strconv.FormatFloat(dy, 'f', 0, 64)
	p.runCmd("ydotool", "mousemove", "-x", dxStr, "-y", dyStr)
}

const (
	evSyn     = 0x00
	evRel     = 0x02
	relX      = 0x00
	relY      = 0x01
	synReport = 0
)

type linuxInputEvent struct {
	TimeSec  int64
	TimeUsec int64
	Type     uint16
	Code     uint16
	Value    int32
}

func findYdotoolSocket() string {
	if s := os.Getenv("YDOTOOL_SOCKET"); s != "" {
		return s
	}
	uid := os.Getuid()
	userSock := fmt.Sprintf("/run/user/%d/.ydotool_socket", uid)
	if _, err := os.Stat(userSock); err == nil {
		return userSock
	}
	if _, err := os.Stat("/tmp/.ydotool_socket"); err == nil {
		return "/tmp/.ydotool_socket"
	}
	return ""
}

func (p *MousepadPlugin) sendYdotoolSocketMove(dx, dy int32) error {
	p.ydotoolMu.Lock()
	defer p.ydotoolMu.Unlock()

	if p.ydotoolConn == nil {
		sock := findYdotoolSocket()
		if sock == "" {
			return errors.New("ydotool socket not found")
		}
		d := net.Dialer{Timeout: 500 * time.Millisecond}
		conn, err := d.DialContext(p.ctx, "unix", sock)
		if err != nil {
			return err
		}
		p.ydotoolConn = conn
	}

	events := [3]linuxInputEvent{
		{Type: evRel, Code: relX, Value: dx},
		{Type: evRel, Code: relY, Value: dy},
		{Type: evSyn, Code: synReport, Value: 0},
	}

	if err := binary.Write(p.ydotoolConn, binary.LittleEndian, events); err != nil {
		_ = p.ydotoolConn.Close()
		p.ydotoolConn = nil
		return err
	}
	return nil
}

func (p *MousepadPlugin) runCmd(name string, arg ...string) {
	ctx, cancel := context.WithTimeout(p.ctx, inputCommandTimeout)
	defer cancel()
	if _, err := p.exec.Run(ctx, name, arg...); err != nil {
		p.logger.Warn("mousepad: input command failed", log.String("cmd", name), log.Error(err))
	}
}

// inputCommandTimeout bounds a stuck display-server tool without interrupting a normal input action.
const inputCommandTimeout = 2 * time.Second

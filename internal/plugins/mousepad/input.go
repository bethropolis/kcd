package mousepad

import (
	"context"
	"os/exec"
	"strconv"

	"github.com/bendahl/uinput"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
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
		// Use display-server tool for text input regardless of uinput backend.
		// "--" ends option parsing (supported by both wtype and xdotool)
		// so phone-provided text starting with '-' can't be misparsed.
		if p.isWayland {
			p.runCmd("wtype", "--", body.Key)
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
	if p.isWayland {
		p.runCmd("wtype", "-k", keyName)
	} else {
		p.runCmd("xdotool", "key", keyName)
	}
}

func (p *MousepadPlugin) runCmd(name string, arg ...string) {
	if out, err := exec.CommandContext(context.Background(), name, arg...).CombinedOutput(); err != nil {
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

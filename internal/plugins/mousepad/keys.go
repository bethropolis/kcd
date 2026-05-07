package mousepad

import (
	"github.com/bendahl/uinput"
)

func mapUinputKey(k int) int {
	switch k {
	case 1:
		return uinput.KeyBackspace
	case 2:
		return uinput.KeyTab
	case 3, 12:
		return uinput.KeyEnter
	case 4:
		return uinput.KeyLeft
	case 5:
		return uinput.KeyUp
	case 6:
		return uinput.KeyRight
	case 7:
		return uinput.KeyDown
	case 8:
		return uinput.KeyPageup
	case 9:
		return uinput.KeyPagedown
	case 10:
		return uinput.KeyHome
	case 11:
		return uinput.KeyEnd
	case 13:
		return uinput.KeyDelete
	case 14:
		return uinput.KeyEsc
	case 21:
		return uinput.KeyF1
	case 22:
		return uinput.KeyF2
	case 23:
		return uinput.KeyF3
	case 24:
		return uinput.KeyF4
	case 25:
		return uinput.KeyF5
	case 26:
		return uinput.KeyF6
	case 27:
		return uinput.KeyF7
	case 28:
		return uinput.KeyF8
	case 29:
		return uinput.KeyF9
	case 30:
		return uinput.KeyF10
	case 31:
		return uinput.KeyF11
	case 32:
		return uinput.KeyF12
	default:
		return -1
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

package digitizer

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"syscall"
)

const (
	uiDevCreate  = 0x5501
	uiDevDestroy = 0x5502
	uiSetEvBit   = 0x40045564
	uiSetKeyBit  = 0x40045565
	uiSetAbsBit  = 0x40045567
	busUsb       = 0x03

	evSyn = 0x00
	evKey = 0x01
	evAbs = 0x03

	btnToolPen    = 0x140
	btnToolRubber = 0x141
	btnTouch      = 0x14a

	absX        = 0x00
	absY        = 0x01
	absPressure = 0x18
	synReport   = 0

	maxPressure = 4096
	absSize     = 64
)

type inputID struct {
	Bustype uint16
	Vendor  uint16
	Product uint16
	Version uint16
}

type uinputUserDev struct {
	Name         [80]byte
	ID           inputID
	FfEffectsMax uint32
	Absmax       [absSize]int32
	Absmin       [absSize]int32
	Absfuzz      [absSize]int32
	Absflat      [absSize]int32
}

type inputEvent struct {
	Time  syscall.Timeval
	Type  uint16
	Code  uint16
	Value int32
}

type uinputTablet struct {
	file *os.File
}

func defaultTabletCreator(width, height int32) (TabletDevice, error) {
	return createUinputTablet("/dev/uinput", width, height)
}

func createUinputTablet(path string, width, height int32) (TabletDevice, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, fmt.Errorf("open uinput: %w", err)
	}

	// Register event types
	for _, ev := range []uintptr{evKey, evAbs, evSyn} {
		if err := ioctl(f, uiSetEvBit, ev); err != nil {
			f.Close()
			return nil, fmt.Errorf("set ev bit %d: %w", ev, err)
		}
	}

	// Register button/key events
	for _, key := range []uintptr{btnToolPen, btnToolRubber, btnTouch} {
		if err := ioctl(f, uiSetKeyBit, key); err != nil {
			f.Close()
			return nil, fmt.Errorf("set key bit %d: %w", key, err)
		}
	}

	// Register absolute axes
	for _, abs := range []uintptr{absX, absY, absPressure} {
		if err := ioctl(f, uiSetAbsBit, abs); err != nil {
			f.Close()
			return nil, fmt.Errorf("set abs bit %d: %w", abs, err)
		}
	}

	var dev uinputUserDev
	copy(dev.Name[:], "kcd-digitizer")
	dev.ID = inputID{
		Bustype: busUsb,
		Vendor:  0x4711,
		Product: 0x0818,
		Version: 1,
	}
	dev.Absmax[absX] = width
	dev.Absmax[absY] = height
	dev.Absmax[absPressure] = maxPressure

	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, &dev); err != nil {
		f.Close()
		return nil, fmt.Errorf("marshal uinput dev: %w", err)
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		f.Close()
		return nil, fmt.Errorf("write uinput dev: %w", err)
	}

	if err := ioctl(f, uiDevCreate, 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("create uinput device: %w", err)
	}

	return &uinputTablet{file: f}, nil
}

func (t *uinputTablet) SendEvent(body DigitizerBody) error {
	var events []inputEvent

	if body.Tool != "" {
		switch body.Tool {
		case "Rubber":
			events = append(events, inputEvent{Type: evKey, Code: btnToolPen, Value: 0})
			events = append(events, inputEvent{Type: evKey, Code: btnToolRubber, Value: 1})
		default: // "Pen"
			events = append(events, inputEvent{Type: evKey, Code: btnToolRubber, Value: 0})
			events = append(events, inputEvent{Type: evKey, Code: btnToolPen, Value: 1})
		}
	}

	if body.Active != nil {
		if !*body.Active {
			events = append(events,
				inputEvent{Type: evKey, Code: btnToolPen, Value: 0},
				inputEvent{Type: evKey, Code: btnToolRubber, Value: 0},
				inputEvent{Type: evKey, Code: btnTouch, Value: 0},
				inputEvent{Type: evAbs, Code: absPressure, Value: 0},
			)
		} else if body.Tool == "" {
			events = append(events, inputEvent{Type: evKey, Code: btnToolPen, Value: 1})
		}
	}

	if body.X != nil {
		events = append(events, inputEvent{Type: evAbs, Code: absX, Value: int32(*body.X)})
	}
	if body.Y != nil {
		events = append(events, inputEvent{Type: evAbs, Code: absY, Value: int32(*body.Y)})
	}
	if body.Pressure != nil {
		p := int32(*body.Pressure * maxPressure)
		if p < 0 {
			p = 0
		} else if p > maxPressure {
			p = maxPressure
		}
		events = append(events, inputEvent{Type: evAbs, Code: absPressure, Value: p})
	}
	if body.Touching != nil {
		touchVal := int32(0)
		if *body.Touching {
			touchVal = 1
		}
		events = append(events, inputEvent{Type: evKey, Code: btnTouch, Value: touchVal})
	}

	events = append(events, inputEvent{Type: evSyn, Code: synReport, Value: 0})

	var buf bytes.Buffer
	for _, ev := range events {
		if err := binary.Write(&buf, binary.LittleEndian, &ev); err != nil {
			return err
		}
	}
	_, err := t.file.Write(buf.Bytes())
	return err
}

func (t *uinputTablet) Close() error {
	_ = ioctl(t.file, uiDevDestroy, 0)
	return t.file.Close()
}

func ioctl(f *os.File, req, arg uintptr) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), req, arg)
	if errno != 0 {
		return errno
	}
	return nil
}

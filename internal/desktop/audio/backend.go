// Package audio provides shared pactl and wpctl access for desktop plugins.
package audio

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/bethropolis/kcd/internal/sysexec"
)

// Backend controls the default desktop audio sink.
type Backend struct {
	exec *sysexec.Exec
	name string
}

// New detects wpctl first, then pactl, using exec for all process access.
func New(exec *sysexec.Exec) *Backend {
	if exec == nil {
		exec = sysexec.New(nil)
	}
	if _, err := exec.LookPath("wpctl"); err == nil {
		return &Backend{exec: exec, name: "wpctl"}
	}
	if _, err := exec.LookPath("pactl"); err == nil {
		return &Backend{exec: exec, name: "pactl"}
	}
	return &Backend{exec: exec}
}

// Available reports whether a supported audio command was found.
func (b *Backend) Available() bool { return b != nil && b.name != "" }

// Name returns the selected command backend.
func (b *Backend) Name() string {
	if b == nil {
		return ""
	}
	return b.name
}

// Read returns the default sink volume and mute state.
func (b *Backend) Read(ctx context.Context) (int, bool, error) {
	if b == nil || b.name == "" {
		return 0, false, fmt.Errorf("audio: no backend available")
	}
	if b.name == "wpctl" {
		out, err := b.exec.Output(ctx, "wpctl", "get-volume", "@DEFAULT_AUDIO_SINK@")
		if err != nil {
			return 0, false, err
		}
		line := strings.TrimSpace(string(out))
		muted := strings.Contains(line, "[MUTED]")
		fields := strings.Fields(strings.ReplaceAll(line, "[MUTED]", ""))
		if len(fields) < 2 {
			return 0, muted, fmt.Errorf("audio: parse wpctl volume %q", line)
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return 0, muted, fmt.Errorf("audio: parse wpctl volume: %w", err)
		}
		return int(value * 100), muted, nil
	}
	out, err := b.exec.Output(ctx, "pactl", "get-sink-volume", "@DEFAULT_SINK@")
	if err != nil {
		return 0, false, err
	}
	volume := -1
	for _, field := range strings.Fields(string(out)) {
		if strings.HasSuffix(field, "%") {
			if value, parseErr := strconv.Atoi(strings.TrimSuffix(field, "%")); parseErr == nil {
				volume = value
				break
			}
		}
	}
	if volume < 0 {
		return 0, false, fmt.Errorf("audio: parse pactl volume")
	}
	muteOut, err := b.exec.Output(ctx, "pactl", "get-sink-mute", "@DEFAULT_SINK@")
	if err != nil {
		return 0, false, err
	}
	return volume, strings.Contains(string(muteOut), "yes"), nil
}

// Set applies volume and mute state to the default sink.
func (b *Backend) Set(ctx context.Context, volume int, muted bool) error {
	if b == nil || b.name == "" {
		return fmt.Errorf("audio: no backend available")
	}
	if volume < 0 || volume > 100 {
		return fmt.Errorf("audio: volume %d outside 0–100", volume)
	}
	if b.name == "wpctl" {
		if _, err := b.exec.Run(ctx, "wpctl", "set-volume", "@DEFAULT_AUDIO_SINK@", strconv.Itoa(volume)+"%"); err != nil {
			return err
		}
		arg := "0"
		if muted {
			arg = "1"
		}
		_, err := b.exec.Run(ctx, "wpctl", "set-mute", "@DEFAULT_AUDIO_SINK@", arg)
		return err
	}
	if _, err := b.exec.Run(ctx, "pactl", "set-sink-volume", "@DEFAULT_SINK@", strconv.Itoa(volume)+"%"); err != nil {
		return err
	}
	arg := "false"
	if muted {
		arg = "true"
	}
	_, err := b.exec.Run(ctx, "pactl", "set-sink-mute", "@DEFAULT_SINK@", arg)
	return err
}

// SetVolume updates only the default sink's volume, preserving its mute state.
func (b *Backend) SetVolume(ctx context.Context, volume int) error {
	if b == nil || b.name == "" {
		return fmt.Errorf("audio: no backend available")
	}
	if volume < 0 || volume > 100 {
		return fmt.Errorf("audio: volume %d outside 0–100", volume)
	}
	if b.name == "wpctl" {
		_, err := b.exec.Run(ctx, "wpctl", "set-volume", "@DEFAULT_AUDIO_SINK@", strconv.Itoa(volume)+"%")
		return err
	}
	_, err := b.exec.Run(ctx, "pactl", "set-sink-volume", "@DEFAULT_SINK@", strconv.Itoa(volume)+"%")
	return err
}

// SetMuted updates only the default sink's mute state.
func (b *Backend) SetMuted(ctx context.Context, muted bool) error {
	if b == nil || b.name == "" {
		return fmt.Errorf("audio: no backend available")
	}
	if b.name == "wpctl" {
		arg := "0"
		if muted {
			arg = "1"
		}
		_, err := b.exec.Run(ctx, "wpctl", "set-mute", "@DEFAULT_AUDIO_SINK@", arg)
		return err
	}
	arg := "false"
	if muted {
		arg = "true"
	}
	_, err := b.exec.Run(ctx, "pactl", "set-sink-mute", "@DEFAULT_SINK@", arg)
	return err
}

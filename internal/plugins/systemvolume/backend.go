package systemvolume

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// getSinks returns a list of available audio output sinks.
func (p *SystemVolumePlugin) getSinks() []SinkInfo {
	switch p.backend {
	case "wpctl":
		return p.getSinksWpctl()
	case "pactl":
		return p.getSinksPactl()
	}
	return nil
}

func (p *SystemVolumePlugin) getSinksWpctl() []SinkInfo {
	// Get current volume from wpctl: wpctl get-volume @DEFAULT_AUDIO_SINK@
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "wpctl", "get-volume", "@DEFAULT_AUDIO_SINK@").Output()
	if err != nil {
		return nil
	}
	// Output: "Volume: 0.75 [MUTED]" or "Volume: 0.75"
	line := strings.TrimSpace(string(out))
	muted := strings.Contains(line, "[MUTED]")
	line = strings.ReplaceAll(line, "[MUTED]", "")
	parts := strings.Fields(line)
	vol := 75
	if len(parts) >= 2 {
		if f, err := strconv.ParseFloat(parts[1], 64); err == nil {
			vol = int(f * 100)
		}
	}
	return []SinkInfo{{
		Name:        "@DEFAULT_AUDIO_SINK@",
		Description: "Default Output",
		Volume:      vol,
		Muted:       muted,
		MaxVolume:   100,
	}}
}

func (p *SystemVolumePlugin) getSinksPactl() []SinkInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "pactl", "get-sink-volume", "@DEFAULT_SINK@").Output()
	if err != nil {
		return nil
	}
	// Very rough parse: look for the first percentage
	vol := 75
	for _, field := range strings.Fields(string(out)) {
		if strings.HasSuffix(field, "%") {
			if v, err := strconv.Atoi(strings.TrimSuffix(field, "%")); err == nil {
				vol = v
				break
			}
		}
	}
	muteCtx, muteCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer muteCancel()
	muteOut, _ := exec.CommandContext(muteCtx, "pactl", "get-sink-mute", "@DEFAULT_SINK@").Output()
	muted := strings.Contains(string(muteOut), "yes")
	return []SinkInfo{{
		Name:        "@DEFAULT_SINK@",
		Description: "Default Output",
		Volume:      vol,
		Muted:       muted,
		MaxVolume:   100,
	}}
}

// setVolume applies volume and mute settings via the detected backend.
func (p *SystemVolumePlugin) setVolume(name string, volume int, muted bool) error {
	return p.setVolumeStr(name, strconv.Itoa(volume), muted)
}

func (p *SystemVolumePlugin) setVolumeStr(_ string, volumeStr string, muted bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	switch p.backend {
	case "wpctl":
		pct := volumeStr + "%"
		if err := exec.CommandContext(ctx, "wpctl", "set-volume", "@DEFAULT_AUDIO_SINK@", pct).Run(); err != nil {
			return err
		}
		muteArg := "0"
		if muted {
			muteArg = "1"
		}
		return exec.CommandContext(ctx, "wpctl", "set-mute", "@DEFAULT_AUDIO_SINK@", muteArg).Run()
	case "pactl":
		if err := exec.CommandContext(ctx, "pactl", "set-sink-volume", "@DEFAULT_SINK@", volumeStr+"%").Run(); err != nil {
			return err
		}
		muteArg := "false"
		if muted {
			muteArg = "true"
		}
		return exec.CommandContext(ctx, "pactl", "set-sink-mute", "@DEFAULT_SINK@", muteArg).Run()
	}
	return nil
}

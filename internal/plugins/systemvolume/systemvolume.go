// Package systemvolume implements the KDE Connect System Volume plugin.
// It allows the phone to query and control the PC's audio volume.
package systemvolume

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

// SystemVolumePlugin handles volume control packets from the phone.
type SystemVolumePlugin struct {
	logger  *zap.Logger
	backend string // "wpctl" or "pactl"
}

func NewSystemVolumePlugin(logger *zap.Logger) *SystemVolumePlugin {
	p := &SystemVolumePlugin{
		logger: logger.With(zap.String("plugin", "systemvolume")),
	}
	// Detect available audio backend at init time.
	if _, err := exec.LookPath("wpctl"); err == nil {
		p.backend = "wpctl"
	} else if _, err := exec.LookPath("pactl"); err == nil {
		p.backend = "pactl"
	} else {
		p.logger.Warn("systemvolume: no audio backend found (wpctl or pactl required)")
	}
	return p
}

type VolumeBody struct {
	RequestSinks bool   `json:"requestSinks,omitempty"`
	Name         string `json:"name,omitempty"`
	Volume       int    `json:"volume,omitempty"`
	Muted        bool   `json:"muted,omitempty"`
	MaxVolume    int    `json:"maxVolume,omitempty"`
}

type SinkInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Volume      int    `json:"volume"`
	Muted       bool   `json:"muted"`
	MaxVolume   int    `json:"maxVolume"`
}

func (p *SystemVolumePlugin) Name() string            { return "SystemVolume" }
func (p *SystemVolumePlugin) Timeout() time.Duration  { return 5 * time.Second }
func (p *SystemVolumePlugin) IncomingTypes() []string { return []string{"kdeconnect.systemvolume"} }
func (p *SystemVolumePlugin) OutgoingTypes() []string { return []string{"kdeconnect.systemvolume"} }

func (p *SystemVolumePlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	if p.backend == "" {
		return nil // No audio backend, silently ignore.
	}

	var body VolumeBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	// Phone is requesting the list of audio sinks.
	if body.RequestSinks {
		go func() {
			sinks := p.getSinks()
			type sinkListBody struct {
				SinkList []SinkInfo `json:"sinkList"`
			}
			pkt, err := protocol.NewPacket("kdeconnect.systemvolume", sinkListBody{SinkList: sinks})
			if err != nil {
				p.logger.Error("systemvolume: failed to create sink list packet", zap.Error(err))
				return
			}
			if err := dev.Send(pkt); err != nil {
				p.logger.Error("systemvolume: failed to send sink list", zap.Error(err))
			}
		}()
		return nil
	}

	// Phone is setting volume or mute.
	go func() {
		if body.Name == "" {
			body.Name = "@DEFAULT_AUDIO_SINK@"
		}
		if err := p.setVolume(body.Name, body.Volume, body.Muted); err != nil {
			p.logger.Warn("systemvolume: failed to set volume", zap.Error(err))
		}
	}()

	return nil
}

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
	out, err := exec.Command("wpctl", "get-volume", "@DEFAULT_AUDIO_SINK@").Output()
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
	out, err := exec.Command("pactl", "get-sink-volume", "@DEFAULT_SINK@").Output()
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
	muteOut, _ := exec.Command("pactl", "get-sink-mute", "@DEFAULT_SINK@").Output()
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

func (p *SystemVolumePlugin) setVolumeStr(name string, volumeStr string, muted bool) error {
	switch p.backend {
	case "wpctl":
		pct := volumeStr + "%"
		if err := exec.Command("wpctl", "set-volume", "@DEFAULT_AUDIO_SINK@", pct).Run(); err != nil {
			return err
		}
		muteArg := "0"
		if muted {
			muteArg = "1"
		}
		return exec.Command("wpctl", "set-mute", "@DEFAULT_AUDIO_SINK@", muteArg).Run()
	case "pactl":
		if err := exec.Command("pactl", "set-sink-volume", "@DEFAULT_SINK@", volumeStr+"%").Run(); err != nil {
			return err
		}
		muteArg := "false"
		if muted {
			muteArg = "true"
		}
		return exec.Command("pactl", "set-sink-mute", "@DEFAULT_SINK@", muteArg).Run()
	}
	return nil
}

func (p *SystemVolumePlugin) OnConnect(dev device.Sender)    {}
func (p *SystemVolumePlugin) OnDisconnect(dev device.Sender) {}

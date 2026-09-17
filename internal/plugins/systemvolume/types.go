// Package systemvolume implements the KDE Connect System Volume plugin.
// It allows the phone to query and control the PC's audio volume.
package systemvolume

import (
	"os/exec"
	"time"

	"github.com/bethropolis/kcd/internal/events"
	"go.uber.org/zap"
)

// SystemVolumePlugin handles volume control packets from the phone.
type SystemVolumePlugin struct {
	logger  *zap.Logger
	bus     *events.Bus
	backend string // "wpctl" or "pactl"
}

func NewSystemVolumePlugin(bus *events.Bus, logger *zap.Logger) *SystemVolumePlugin {
	p := &SystemVolumePlugin{
		logger: logger.With(zap.String("plugin", "systemvolume")),
		bus:    bus,
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

// sinkListBody is the outbound sink list payload.
type sinkListBody struct {
	SinkList []SinkInfo `json:"sinkList"`
}

func (p *SystemVolumePlugin) Name() string           { return "SystemVolume" }
func (p *SystemVolumePlugin) Timeout() time.Duration { return 5 * time.Second }
func (p *SystemVolumePlugin) IncomingTypes() []string {
	return []string{"kdeconnect.systemvolume.request"}
}
func (p *SystemVolumePlugin) OutgoingTypes() []string { return []string{"kdeconnect.systemvolume"} }

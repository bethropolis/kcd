// Package systemvolume implements the KDE Connect System Volume plugin.
// It allows the phone to query and control the PC's audio volume.
package systemvolume

import (
	"time"

	"github.com/bethropolis/kcd/internal/desktop/audio"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
)

// SystemVolumePlugin handles volume control packets from the phone.
type SystemVolumePlugin struct {
	logger log.Logger
	bus    *events.Bus
	audio  *audio.Backend
}

func NewSystemVolumePlugin(bus *events.Bus, logger log.Logger, backends ...*audio.Backend) *SystemVolumePlugin {
	p := &SystemVolumePlugin{
		logger: logger.With(log.String("plugin", "systemvolume")),
		bus:    bus,
	}
	if len(backends) > 0 && backends[0] != nil {
		p.audio = backends[0]
	} else {
		p.audio = audio.New(nil)
	}
	if !p.audio.Available() {
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

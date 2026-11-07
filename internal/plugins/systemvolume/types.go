// Package systemvolume implements the KDE Connect System Volume plugin.
// It allows the phone to query and control the PC's audio volume.
package systemvolume

import (
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

type VolumeBody struct {
	RequestSinks bool   `json:"requestSinks,omitempty"`
	Name         string `json:"name,omitempty"`
	Volume       *int   `json:"volume,omitempty"`
	Muted        *bool  `json:"muted,omitempty"`
	Enabled      *bool  `json:"enabled,omitempty"`
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

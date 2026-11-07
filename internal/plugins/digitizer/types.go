package digitizer

import (
	"sync"

	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
)

// SessionBody represents a kdeconnect.digitizer.session packet.
type SessionBody struct {
	Action      string  `json:"action"` // "start" or "end"
	Width       int32   `json:"width,omitempty"`
	Height      int32   `json:"height,omitempty"`
	ResolutionX float64 `json:"resolutionX,omitempty"`
	ResolutionY float64 `json:"resolutionY,omitempty"`
}

// DigitizerBody represents a kdeconnect.digitizer event packet.
type DigitizerBody struct {
	Active   *bool    `json:"active,omitempty"`
	Touching *bool    `json:"touching,omitempty"`
	Tool     string   `json:"tool,omitempty"` // "Pen", "Rubber"
	X        *float64 `json:"x,omitempty"`
	Y        *float64 `json:"y,omitempty"`
	Pressure *float64 `json:"pressure,omitempty"`
}

// TabletDevice is the interface for injecting drawing tablet events into the host OS.
type TabletDevice interface {
	SendEvent(body DigitizerBody) error
	Close() error
}

// TabletCreator creates a new TabletDevice for a session with given dimensions.
type TabletCreator func(width, height int32) (TabletDevice, error)

// DigitizerPlugin handles drawing tablet input from the mobile peer.
type DigitizerPlugin struct {
	bus      *events.Bus
	logger   log.Logger
	creator  TabletCreator
	mu       sync.Mutex
	sessions map[string]TabletDevice // deviceID -> active TabletDevice
}

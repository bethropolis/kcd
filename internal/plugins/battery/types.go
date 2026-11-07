package battery

import (
	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/sysexec"
)

// ThresholdEvent values from the KDE Connect protocol.
const (
	thresholdNone = 0
	thresholdLow  = 1 // battery is low (typically <= 15%)
	thresholdFull = 2 // battery reached full charge (kcd extension)
)

// BatteryPlugin handles incoming battery state updates.
type BatteryPlugin struct {
	notifications config.NotificationConfig
	cfg           config.BatteryConfig
	bus           *events.Bus
	logger        log.Logger
	exec          *sysexec.Exec
}

// BatteryBody represents a kdeconnect.battery body. Request-only asks the peer
// to report; it is not a state update and must never touch stored charge.
type BatteryBody struct {
	CurrentCharge  int  `json:"currentCharge"`
	IsCharging     bool `json:"isCharging"`
	ThresholdEvent int  `json:"thresholdEvent"`
	Request        bool `json:"request,omitempty"`
}

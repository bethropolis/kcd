package daemon

import (
	"crypto/tls"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
)

// transportEnv consolidates shared dependencies across the transport subsystem.
type transportEnv struct {
	tls      *tls.Config
	identity *protocol.Packet
	devices  *device.Registry
	plugins  *plugin.Registry
	localID  string
	cfg      *config.Config
	logger   log.Logger
}

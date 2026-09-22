package config

import (
	"fmt"
	"time"
)

// NetworkConfig controls connection setup deadlines, not transfer size limits.
// TransferIdleTimeout bounds streaming silence on side-channel transfers:
// any read/write gap longer than it aborts the transfer.
type NetworkConfig struct {
	DialTimeout         string `toml:"dial_timeout"`
	HandshakeTimeout    string `toml:"handshake_timeout"`
	SidechannelTimeout  string `toml:"sidechannel_timeout"`
	TransferIdleTimeout string `toml:"transfer_idle_timeout"`
}

// ReconnectConfig controls retry delays and the minimum stable connection age.
type ReconnectConfig struct {
	InitialBackoff string `toml:"initial_backoff"`
	MaxBackoff     string `toml:"max_backoff"`
	FlapThreshold  string `toml:"flap_threshold"`
	// SightingDriven parks the redial timer on discovery sightings: while
	// set, the loop dials immediately on sighting and otherwise waits up
	// to FallbackMax, giving up entirely past StaleAfter. False restores
	// the legacy pure-timer loop capped at MaxBackoff.
	SightingDriven bool   `toml:"sighting_driven"`
	FallbackMax    string `toml:"fallback_max"`
	StaleAfter     string `toml:"stale_after"`
}

// DiscoveryConfig controls intervals while on-demand UDP discovery is running.
type DiscoveryConfig struct {
	BroadcastInterval     string `toml:"broadcast_interval"`
	BroadcastIdleInterval string `toml:"broadcast_idle_interval"`
}

// MPRISConfig controls local media polling. The D-Bus watcher itself is
// event-driven; the position poller only runs while music plays.
type MPRISConfig struct {
	PollWhilePlaying bool   `toml:"poll_while_playing"`
	PositionInterval string `toml:"position_interval"`
}

// CacheConfig overrides storage directories. Empty values retain plugin defaults.
type CacheConfig struct {
	SMSAttachmentsDir string `toml:"sms_attachments_dir"`
	AlbumArtDir       string `toml:"album_art_dir"`
	ContactsDir       string `toml:"contacts_dir"`
}

// Duration parses a duration from a validated Config. Call Load or Validate first.
// Invalid input is a programming error; validation supplies user-facing errors.
func Duration(value string) time.Duration {
	d, err := time.ParseDuration(value)
	if err != nil {
		panic(fmt.Sprintf("config: unvalidated duration %q: %v", value, err))
	}
	return d
}

func (c *Config) validateDurations() error {
	for _, setting := range []struct{ name, value string }{
		{"network.dial_timeout", c.Network.DialTimeout},
		{"network.handshake_timeout", c.Network.HandshakeTimeout},
		{"network.sidechannel_timeout", c.Network.SidechannelTimeout},
		{"network.transfer_idle_timeout", c.Network.TransferIdleTimeout},
		{"reconnect.initial_backoff", c.Reconnect.InitialBackoff},
		{"reconnect.max_backoff", c.Reconnect.MaxBackoff},
		{"reconnect.flap_threshold", c.Reconnect.FlapThreshold},
		{"reconnect.fallback_max", c.Reconnect.FallbackMax},
		{"reconnect.stale_after", c.Reconnect.StaleAfter},
		{"discovery.broadcast_interval", c.Discovery.BroadcastInterval},
		{"discovery.broadcast_idle_interval", c.Discovery.BroadcastIdleInterval},
		{"pairing.intent_ttl", c.Pairing.IntentTTL},
		{"pairing.listen_timeout", c.Pairing.ListenTimeout},
		{"mpris.position_interval", c.MPRIS.PositionInterval},
	} {
		d, err := time.ParseDuration(setting.value)
		if err != nil {
			return fmt.Errorf("config: invalid %s %q: %w", setting.name, setting.value, err)
		}
		if d <= 0 {
			return fmt.Errorf("config: %s must be greater than zero", setting.name)
		}
	}
	if Duration(c.Reconnect.MaxBackoff) < Duration(c.Reconnect.InitialBackoff) {
		return fmt.Errorf("config: reconnect.max_backoff must be >= reconnect.initial_backoff")
	}
	if Duration(c.Reconnect.FallbackMax) < Duration(c.Reconnect.MaxBackoff) {
		return fmt.Errorf("config: reconnect.fallback_max must be >= reconnect.max_backoff")
	}
	if Duration(c.Discovery.BroadcastIdleInterval) < Duration(c.Discovery.BroadcastInterval) {
		return fmt.Errorf("config: discovery.broadcast_idle_interval must be >= discovery.broadcast_interval")
	}
	return nil
}

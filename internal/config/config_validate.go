package config

import (
	"fmt"
	"time"

	"github.com/bethropolis/kcd/internal/protocol"
)

// Validate checks required fields and returns an error if any are invalid.
func (c *Config) Validate() error {
	if err := c.validateDurations(); err != nil {
		return err
	}
	if c.RunCommand.DefaultTimeout <= 0 {
		return fmt.Errorf("config: runcommand default_timeout must be positive")
	}
	for event, hook := range c.Hooks {
		if event == "" || len(hook.Command) == 0 || hook.Command[0] == "" {
			return fmt.Errorf("config: hook %q must have a command", event)
		}
		if hook.Timeout < 0 || hook.Debounce < 0 {
			return fmt.Errorf("config: hook %q durations cannot be negative", event)
		}
	}
	if c.Backends.Volume != "auto" && c.Backends.Volume != "pactl" && c.Backends.Volume != "wpctl" && c.Backends.Volume != "none" {
		return fmt.Errorf("config: invalid volume backend %q", c.Backends.Volume)
	}
	if c.Backends.Notify != "auto" && c.Backends.Notify != "notify-send" && c.Backends.Notify != "dbus" && c.Backends.Notify != "none" {
		return fmt.Errorf("config: invalid notify backend %q", c.Backends.Notify)
	}
	if c.Popups.Backend != "auto" && c.Popups.Backend != "notify-send" && c.Popups.Backend != "dbus" && c.Popups.Backend != "none" {
		return fmt.Errorf("config: invalid popup backend %q", c.Popups.Backend)
	}
	if protocol.SanitizeDeviceName(c.DeviceName) == "" {
		return fmt.Errorf("config: device_name is required")
	}
	switch c.DeviceType {
	case "desktop", "laptop", "phone", "tablet", "tv":
		// valid
	default:
		return fmt.Errorf("config: invalid device_type %q (expected desktop, laptop, phone, tablet, tv)", c.DeviceType)
	}
	if c.TCPPort < protocol.TCPPortMin || c.TCPPort > protocol.TCPPortMax {
		return fmt.Errorf("config: tcp_port must be %d-%d, got %d", protocol.TCPPortMin, protocol.TCPPortMax, c.TCPPort)
	}

	// Battery urgency validation
	for _, u := range []string{c.Battery.LowUrgency, c.Battery.FullUrgency} {
		if u != "" {
			switch u {
			case "low", "normal", "critical":
			default:
				return fmt.Errorf("config: invalid battery urgency %q (expected low, normal, critical)", u)
			}
		}
	}

	// Share port range validation
	if c.Share.PortMin < 1 || c.Share.PortMin > 65535 || c.Share.PortMax < 1 || c.Share.PortMax > 65535 {
		return fmt.Errorf("config: share ports must be 1-65535")
	}
	if c.Share.PortMin > c.Share.PortMax {
		return fmt.Errorf("config: share port_min (%d) cannot be greater than port_max (%d)", c.Share.PortMin, c.Share.PortMax)
	}
	if c.Share.MaxFileSize <= 0 || c.Share.MaxFileSize > 1<<50 {
		return fmt.Errorf("config: share max_file_size must be between 1 and 1125899906842624 bytes")
	}

	// Prune threshold validation
	if c.PruneStaleThreshold != "" {
		if _, err := time.ParseDuration(c.PruneStaleThreshold); err != nil {
			return fmt.Errorf("config: invalid prune_stale_threshold %q: %w", c.PruneStaleThreshold, err)
		}
	}

	// Mousepad backend validation
	switch c.Mousepad.Backend {
	case "auto", "ydotool", "xdotool", "uinput":
	default:
		return fmt.Errorf("config: invalid mousepad backend %q (expected auto, ydotool, xdotool)", c.Mousepad.Backend)
	}

	return nil
}

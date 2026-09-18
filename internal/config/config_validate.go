package config

import (
	"fmt"
	"time"
)

// Validate checks required fields and returns an error if any are invalid.
func (c *Config) Validate() error {
	if err := c.validateDurations(); err != nil {
		return err
	}
	if c.DeviceName == "" {
		return fmt.Errorf("config: device_name is required")
	}
	switch c.DeviceType {
	case "desktop", "laptop", "phone", "tablet", "tv":
		// valid
	default:
		return fmt.Errorf("config: invalid device_type %q (expected desktop, laptop, phone, tablet, tv)", c.DeviceType)
	}
	if c.TCPPort < 1 || c.TCPPort > 65535 {
		return fmt.Errorf("config: tcp_port must be 1-65535, got %d", c.TCPPort)
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

// Package config handles loading and validating the kcd daemon configuration.
// It has zero external imports except github.com/BurntSushi/toml.
package config

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config holds all daemon configuration.
type Config struct {
	DeviceID          string                   `toml:"device_id"`
	DeviceName        string                   `toml:"device_name"`
	DeviceType        string                   `toml:"device_type"` // "desktop", "laptop", "phone", "tablet"
	CertFile          string                   `toml:"cert_file"`
	KeyFile           string                   `toml:"key_file"`
	SocketPath        string                   `toml:"socket_path"`
	DownloadDir       string                   `toml:"download_dir"`
	TCPPort           int                      `toml:"tcp_port"`
	EnableBroadcast   bool                     `toml:"enable_broadcast"`    // Toggle UDP discovery broadcast
	LogLevel          string                   `toml:"log_level"`           // "debug", "info", "warn", "error" (or "quiet")
	AutoAcceptPairing bool                     `toml:"auto_accept_pairing"` // Auto-accept incoming pair requests (headless mode)
	Plugins           PluginConfig             `toml:"plugins"`
	Commands          map[string]string        `toml:"commands"`
	Notifications     NotificationConfig       `toml:"notifications"`
	Battery           BatteryConfig            `toml:"battery"`
	Notification      NotificationPluginConfig `toml:"notification_plugin"`
	Share             ShareConfig              `toml:"share"`
	SFTP              SFTPConfig               `toml:"sftp"`
	Ping              PingConfig               `toml:"ping"`
	Pairing           PairingConfig            `toml:"pairing"`
	Mousepad          MousepadConfig           `toml:"mousepad"`
	ConfigPath        string                   `toml:"-"` // populated at load time, never written to disk
}

// PluginConfig toggles individual plugins on or off.

// Defaults returns a Config populated with sensible defaults using XDG paths.
func Defaults() *Config {
	home, _ := os.UserHomeDir()
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "kcd-desktop"
	}

	c := &Config{}
	c.DeviceName = hostname
	c.DeviceType = "desktop"
	c.CertFile = configPath("cert.pem", false)
	c.KeyFile = configPath("key.pem", false)
	c.SocketPath = DefaultSocketPath()
	c.DownloadDir = filepath.Join(home, "Downloads", "kcd")
	c.TCPPort = 1716
	c.EnableBroadcast = true
	c.LogLevel = "info"

	c.Plugins.Defaults()
	c.Commands = make(map[string]string)
	c.Battery.Defaults()
	c.Notification.Defaults()
	c.Share.Defaults()
	c.SFTP.Defaults()
	c.Ping.Defaults()
	c.Pairing.Defaults()
	c.Mousepad.Defaults()

	return c
}

// Load reads a TOML config file and merges it with defaults.
func Load(path string) (*Config, error) {
	cfg := Defaults()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// No config file — use all defaults.
			return cfg, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	cfg.ConfigPath = path
	return cfg, nil
}

// Validate checks required fields and returns an error if any are invalid.
func (c *Config) Validate() error {
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

	// Mousepad backend validation
	switch c.Mousepad.Backend {
	case "auto", "ydotool", "xdotool", "uinput":
	default:
		return fmt.Errorf("config: invalid mousepad backend %q (expected auto, ydotool, xdotool)", c.Mousepad.Backend)
	}

	return nil
}

// EnsureDeviceID generates a UUIDv4-style device ID if one is not already set,
// and writes the updated config back to the given path.
func (c *Config) EnsureDeviceID(configPath string) error {
	if c.DeviceID != "" {
		return nil
	}

	id, err := generateDeviceID()
	if err != nil {
		return fmt.Errorf("config: generate device id: %w", err)
	}
	c.DeviceID = id

	// Persist the generated ID if a config path is provided.
	if configPath != "" {
		if err := c.Save(configPath); err != nil {
			return fmt.Errorf("config: save after generating device id: %w", err)
		}
	}
	return nil
}

// Save writes the config to a TOML file, creating parent directories as needed.
func (c *Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("config: create dir %s: %w", dir, err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("config: open %s: %w", path, err)
	}
	defer f.Close()

	enc := toml.NewEncoder(f)
	if err := enc.Encode(c); err != nil {
		return fmt.Errorf("config: encode: %w", err)
	}
	return nil
}

// StatePath returns the path to the device state file.
func StatePath() string {
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		home, _ := os.UserHomeDir()
		stateHome = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(stateHome, "kcd", "devices.json")
}

// DefaultConfigPath returns the default config file path.
func DefaultConfigPath() string {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, _ := os.UserHomeDir()
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "kcd", "kcd.toml")
}

// DefaultSocketPath returns the default IPC socket path.
func DefaultSocketPath() string {
	uid := fmt.Sprintf("%d", os.Getuid())
	rtDir := os.Getenv("XDG_RUNTIME_DIR")
	if rtDir == "" {
		rtDir = filepath.Join("/run/user", uid)
	}
	return filepath.Join(rtDir, "kcd", "kcd.sock")
}

// configPath returns a path in the kcd config directory.
func configPath(filename string, isRuntime bool) string {
	if isRuntime {
		return filepath.Join(DefaultSocketPath())
	}
	dir := filepath.Dir(DefaultConfigPath())
	return filepath.Join(dir, filename)
}

// NotificationConfig controls per-app notification filtering.
type NotificationConfig map[string]string

// generateDeviceID produces a UUIDv4 with dashes replaced by underscores.
func generateDeviceID() (string, error) {
	var uuid [16]byte
	if _, err := rand.Read(uuid[:]); err != nil {
		return "", err
	}
	uuid[6] = (uuid[6] & 0x0f) | 0x40
	uuid[8] = (uuid[8] & 0x3f) | 0x80

	s := fmt.Sprintf("%x-%x-%x-%x-%x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])

	return strings.ReplaceAll(s, "-", "_"), nil
}

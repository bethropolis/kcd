package config

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

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

// NotificationConfig controls notification branding and per-app filtering.
// The reserved app_name key is not a filter.
type NotificationConfig map[string]string

// AppName returns the desktop notification application name.
func (c NotificationConfig) AppName() string {
	if name := c["app_name"]; name != "" {
		return name
	}
	return "KDE Connect"
}

// Filters returns an independent copy containing only per-app filter entries.
func (c NotificationConfig) Filters() NotificationConfig {
	filters := make(NotificationConfig, len(c))
	for app, action := range c {
		if app != "app_name" {
			filters[app] = action
		}
	}
	return filters
}

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

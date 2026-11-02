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
// and persists it separately from the user's TOML file.
func (c *Config) EnsureDeviceID(configPath string) error {
	if c.DeviceID != "" {
		return nil
	}
	if configPath != "" {
		idPath := deviceIDPath(configPath)
		if data, err := os.ReadFile(idPath); err == nil {
			if id := strings.TrimSpace(string(data)); id != "" {
				c.DeviceID = id
				return nil
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("config: read device id %s: %w", idPath, err)
		}
	}

	id, err := generateDeviceID()
	if err != nil {
		return fmt.Errorf("config: generate device id: %w", err)
	}
	c.DeviceID = id

	if configPath != "" {
		if err := writeDeviceID(deviceIDPath(configPath), id); err != nil {
			return fmt.Errorf("config: save device id: %w", err)
		}
	}
	return nil
}

func deviceIDPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "device_id")
}

func writeDeviceID(path, id string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("config: create dir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".device-id-*")
	if err != nil {
		return fmt.Errorf("config: temp device id: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("config: chmod device id: %w", err)
	}
	if _, err := tmp.WriteString(id + "\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("config: write device id: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("config: sync device id: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: close device id: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("config: rename device id: %w", err)
	}
	return nil
}

// Save writes the config to a TOML file, creating parent directories as needed.
// The write goes to a temp file renamed into place, so a crash mid-write
// leaves either the old config or the new one, never a truncation.
func (c *Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("config: create dir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("config: temp %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	enc := toml.NewEncoder(tmp)
	if err := enc.Encode(c); err != nil {
		tmp.Close()
		return fmt.Errorf("config: encode: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("config: sync %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: close %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("config: rename %s: %w", path, err)
	}
	return nil
}

// StateDir returns kcd's state directory.
func StateDir() string {
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		home, _ := os.UserHomeDir()
		stateHome = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(stateHome, "kcd")
}

// StatePath returns the path to the device state file.
func StatePath() string {
	return filepath.Join(StateDir(), "devices.json")
}

// DefaultMountDir puts mounts where nothing routinely deletes them: mounts
// expose the phone's storage to rm, backups, and cleaners, so the tmpfs
// runtime dir wins and the state dir is only a session-less fallback.
func DefaultMountDir() string {
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		return filepath.Join(runtimeDir, "kcd", "mnt")
	}
	return filepath.Join(StateDir(), "mnt")
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

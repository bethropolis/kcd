package config

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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

// WriteAPIDropIn atomically writes configuration values from the IPC API.
func WriteAPIDropIn(path string, values map[string]any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("config: create drop-in directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".ipc-*.toml")
	if err != nil {
		return fmt.Errorf("config: create drop-in: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("config: chmod drop-in: %w", err)
	}
	if err := toml.NewEncoder(tmp).Encode(values); err != nil {
		tmp.Close()
		return fmt.Errorf("config: encode drop-in: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("config: sync drop-in: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: close drop-in: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("config: install drop-in: %w", err)
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
	return instanceDir(filepath.Join(stateHome, "kcd"))
}

// StatePath returns the path to the device state file.
func StatePath() string {
	return filepath.Join(StateDir(), "devices.json")
}

// DefaultCacheDir returns the cache base directory for the active instance.
func DefaultCacheDir() string {
	cacheHome := os.Getenv("XDG_CACHE_HOME")
	if cacheHome == "" {
		home, _ := os.UserHomeDir()
		cacheHome = filepath.Join(home, ".cache")
	}
	return instanceDir(filepath.Join(cacheHome, "kcd"))
}

// DefaultRuntimeDir returns the runtime base directory for the active instance.
func DefaultRuntimeDir() string {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		runtimeDir = filepath.Join("/run/user", fmt.Sprintf("%d", os.Getuid()))
	}
	return instanceDir(filepath.Join(runtimeDir, "kcd"))
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
	return filepath.Join(instanceDir(filepath.Join(configHome, "kcd")), "kcd.toml")
}

// DefaultSocketPath returns the default IPC socket path.
func DefaultSocketPath() string {
	return filepath.Join(DefaultRuntimeDir(), "kcd.sock")
}

func instanceDir(base string) string {
	instance := strings.TrimSpace(os.Getenv("KCD_INSTANCE"))
	if instance == "" {
		return base
	}
	instance = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, instance)
	return base + "-" + instance
}

func applyEnvOverrides(cfg *Config) error {
	for _, entry := range os.Environ() {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "KCD_") || parts[0] == "KCD_CONFIG" || parts[0] == "KCD_LOG_LEVEL" || parts[0] == "KCD_INSTANCE" {
			continue
		}
		path := strings.SplitN(strings.ToLower(strings.TrimPrefix(parts[0], "KCD_")), "__", 2)
		if len(path) != 2 {
			continue
		}
		if err := setEnvField(reflect.ValueOf(cfg).Elem(), path[0], path[1], parts[1]); err != nil {
			return fmt.Errorf("config: environment override %s: %w", parts[0], err)
		}
	}
	return nil
}

func setEnvField(root reflect.Value, section, key, raw string) error {
	for i := 0; i < root.NumField(); i++ {
		field := root.Type().Field(i)
		if strings.Split(field.Tag.Get("toml"), ",")[0] != section {
			continue
		}
		target := root.Field(i)
		if target.Kind() != reflect.Struct {
			return fmt.Errorf("%s is not a section", section)
		}
		for j := 0; j < target.NumField(); j++ {
			child := target.Type().Field(j)
			if strings.Split(child.Tag.Get("toml"), ",")[0] != key {
				continue
			}
			return setReflectValue(target.Field(j), raw)
		}
		return fmt.Errorf("unknown key %s.%s", section, key)
	}
	return fmt.Errorf("unknown section %s", section)
}

func setReflectValue(target reflect.Value, raw string) error {
	switch target.Kind() {
	case reflect.String:
		target.SetString(raw)
	case reflect.Bool:
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		target.SetBool(value)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value, err := strconv.ParseInt(raw, 10, target.Type().Bits())
		if err != nil {
			return err
		}
		target.SetInt(value)
	default:
		return fmt.Errorf("unsupported value type %s", target.Type())
	}
	return nil
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

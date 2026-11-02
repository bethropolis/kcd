// Package config loads and validates daemon configuration. It imports only
// BurntSushi/toml and stdlib-only protocol, so no import cycle is possible.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/bethropolis/kcd/internal/protocol"
)

// Config holds all daemon configuration.
type Config struct {
	ConfigVersion int       `toml:"config_version"`
	DeviceID      string    `toml:"device_id"`
	DeviceName    string    `toml:"device_name"`
	DeviceType    string    `toml:"device_type"` // "desktop", "laptop", "phone", "tablet"
	CertFile      string    `toml:"cert_file"`
	KeyFile       string    `toml:"key_file"`
	SocketPath    string    `toml:"socket_path"`
	StateDir      string    `toml:"state_dir" json:"-"`
	CacheDir      string    `toml:"cache_dir" json:"-"`
	RuntimeDir    string    `toml:"runtime_dir" json:"-"`
	IPC           IPCConfig `toml:"ipc" json:"-"`
	DownloadDir   string    `toml:"download_dir"`
	TCPPort       int       `toml:"tcp_port"`
	// EnableBroadcast was removed — broadcast starts automatically with `kcd pair` (listen mode)
	LogLevel string `toml:"log_level"` // "debug", "info", "warn", "error" (or "quiet")
	// AutoAcceptPairing was removed in favor of `kcd pair` (listen mode).
	// Old config values are silently ignored by the TOML parser.
	Network             NetworkConfig                `toml:"network"`
	Reconnect           ReconnectConfig              `toml:"reconnect"`
	Discovery           DiscoveryConfig              `toml:"discovery"`
	Cache               CacheConfig                  `toml:"cache"`
	Plugins             PluginConfig                 `toml:"plugins"`
	Commands            map[string]string            `toml:"commands"`
	CommandsPerDevice   map[string]map[string]string `toml:"commands_per_device"`
	Hooks               map[string]HookConfig        `toml:"hooks" json:"-"`
	RunCommand          RunCommandConfig             `toml:"runcommand" json:"-"`
	Backends            BackendsConfig               `toml:"backends" json:"-"`
	Devices             map[string]DeviceConfig      `toml:"devices" json:"-"`
	Notifications       NotificationConfig           `toml:"notifications"`
	Popups              PopupsConfig                 `toml:"popups" json:"-"`
	Battery             BatteryConfig                `toml:"battery"`
	MPRIS               MPRISConfig                  `toml:"mpris"`
	Notification        NotificationPluginConfig     `toml:"notification_plugin"`
	LockDevice          LockDeviceConfig             `toml:"lockdevice"`
	Share               ShareConfig                  `toml:"share"`
	SFTP                SFTPConfig                   `toml:"sftp"`
	Ping                PingConfig                   `toml:"ping"`
	Clipboard           ClipboardConfig              `toml:"clipboard"`
	Pairing             PairingConfig                `toml:"pairing"`
	Mousepad            MousepadConfig               `toml:"mousepad"`
	SMS                 SMSConfig                    `toml:"sms"`
	PruneStaleThreshold string                       `toml:"prune_stale_threshold"` // auto-remove stale unpaired devices; Go duration, "0" = disable

	ConfigPath string   `toml:"-"` // populated at load time, never written to disk
	Warnings   []string `toml:"-" json:"-"`
}

// IPCConfig controls optional local IPC access tiers.
type IPCConfig struct {
	ReadOnlySocket string `toml:"readonly_socket"`
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
	c.ConfigVersion = 1
	c.DeviceName = hostname
	c.DeviceType = "desktop"
	c.CertFile = configPath("cert.pem", false)
	c.KeyFile = configPath("key.pem", false)
	c.SocketPath = DefaultSocketPath()
	c.StateDir = StateDir()
	c.CacheDir = DefaultCacheDir()
	c.RuntimeDir = DefaultRuntimeDir()
	c.DownloadDir = filepath.Join(home, "Downloads", "kcd")
	c.TCPPort = protocol.DefaultTCPPort
	c.LogLevel = "info"

	c.Network = NetworkConfig{DialTimeout: "5s", HandshakeTimeout: "10s", SidechannelTimeout: "15s", TransferIdleTimeout: "60s", KeepAliveIdle: "30s"}
	c.Reconnect = ReconnectConfig{InitialBackoff: "2s", MaxBackoff: "5m", FlapThreshold: "15s", SightingDriven: true, FallbackMax: "1h", StaleAfter: "24h"}
	c.Discovery = DiscoveryConfig{BroadcastInterval: "30s", BroadcastIdleInterval: "60s"}
	c.Plugins.Defaults()
	c.Commands = make(map[string]string)
	c.CommandsPerDevice = make(map[string]map[string]string)
	c.Hooks = make(map[string]HookConfig)
	c.RunCommand.Defaults()
	c.Backends.Defaults()
	c.Devices = make(map[string]DeviceConfig)
	c.Battery.Defaults()
	c.MPRIS = MPRISConfig{PollWhilePlaying: true, PositionInterval: "2s"}
	c.Notification.Defaults()
	c.Popups.Defaults()
	c.Share.Defaults()
	c.SFTP.Defaults()
	c.Ping.Defaults()
	c.Pairing.Defaults()
	c.Mousepad.Defaults()
	c.Clipboard.Defaults()
	c.SMS.Defaults()
	c.PruneStaleThreshold = "15m"

	return c
}

// PluginEnabledForDevice reports whether a globally enabled plugin is allowed
// for a device. Per-device configuration can only restrict global settings.
func (c *Config) PluginEnabledForDevice(deviceID, pluginName string) bool {
	if !globalPluginEnabled(c.Plugins, strings.ToLower(pluginName)) {
		return false
	}
	override, ok := c.Devices[deviceID]
	if !ok || override.Plugins == nil {
		return true
	}
	enabled, ok := override.Plugins[strings.ToLower(pluginName)]
	return !ok || enabled
}

func globalPluginEnabled(p PluginConfig, name string) bool {
	values := map[string]bool{
		"pair":    true,
		"battery": p.Battery, "clipboard": p.Clipboard, "notification": p.Notification,
		"share": p.Share, "runcommand": p.RunCommand, "mpris": p.MPRIS, "ping": p.Ping,
		"telephony": p.Telephony, "connectivity": p.Connectivity, "mousepad": p.Mousepad,
		"sftp": p.SFTP, "findmyphone": p.FindMyPhone, "lockdevice": p.LockDevice,
		"systemvolume": p.SystemVolume, "pausemusic": p.PauseMusic, "sms": p.SMS,
		"contacts": p.Contacts, "presenter": p.Presenter, "findthisdevice": p.FindThisDevice,
		"remotesystemvolume": p.RemoteSystemVolume,
	}
	return values[name]
}

// Load reads a TOML config file and merges it with defaults.
func Load(path string) (*Config, error) {
	cfg := Defaults()
	cfg.ConfigPath = path
	for _, candidate := range configFiles(path) {
		data, err := os.ReadFile(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("config: read %s: %w", candidate, err)
		}
		metadata, err := toml.Decode(string(data), cfg)
		if err != nil {
			return nil, fmt.Errorf("config: parse %s: %w", candidate, err)
		}
		cfg.Warnings = append(cfg.Warnings, unknownKeyWarnings(candidate, metadata.Undecoded())...)
	}
	if err := applyEnvOverrides(cfg); err != nil {
		return nil, err
	}
	cfg.normalizeDurationAliases()

	if data, err := os.ReadFile(deviceIDPath(path)); err == nil {
		fileID := strings.TrimSpace(string(data))
		if cfg.DeviceID == "" {
			cfg.DeviceID = fileID
		} else if fileID != "" && fileID != cfg.DeviceID {
			cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("%s: device_id overrides adjacent device_id file", path))
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("config: read device id: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// StatePath returns the configured device registry path.
func (c *Config) StatePath() string { return filepath.Join(c.StateDir, "devices.json") }

func (c *Config) normalizeDurationAliases() {
	if c.Share.AcceptTimeout != 0 {
		c.Share.AcceptTimeoutSecs = int(c.Share.AcceptTimeout.duration() / time.Second)
	} else {
		c.Share.AcceptTimeout = FlexDuration(time.Duration(c.Share.AcceptTimeoutSecs) * time.Second)
	}
	if c.SFTP.CredentialsTimeout != 0 {
		c.SFTP.CredentialsTimeoutSecs = int(c.SFTP.CredentialsTimeout.duration() / time.Second)
	} else {
		c.SFTP.CredentialsTimeout = FlexDuration(time.Duration(c.SFTP.CredentialsTimeoutSecs) * time.Second)
	}
	if c.SFTP.KeepaliveInterval != 0 {
		c.SFTP.KeepaliveIntervalSecs = int(c.SFTP.KeepaliveInterval.duration() / time.Second)
	} else {
		c.SFTP.KeepaliveInterval = FlexDuration(time.Duration(c.SFTP.KeepaliveIntervalSecs) * time.Second)
	}
	if c.Pairing.Timeout != 0 {
		c.Pairing.TimeoutSecs = int(c.Pairing.Timeout.duration() / time.Second)
	} else {
		c.Pairing.Timeout = FlexDuration(time.Duration(c.Pairing.TimeoutSecs) * time.Second)
	}
	if c.Notification.Expire != 0 {
		c.Notification.ExpireMS = int(c.Notification.Expire.duration() / time.Millisecond)
	} else {
		c.Notification.Expire = FlexDuration(time.Duration(c.Notification.ExpireMS) * time.Millisecond)
	}
	if c.Notification.CancelGrace != 0 {
		c.Notification.CancelGraceMS = int(c.Notification.CancelGrace.duration() / time.Millisecond)
	} else {
		c.Notification.CancelGrace = FlexDuration(time.Duration(c.Notification.CancelGraceMS) * time.Millisecond)
	}
}

func configFiles(path string) []string {
	var files []string
	if path == DefaultConfigPath() {
		files = append(files, "/etc/kcd/kcd.toml")
		files = append(files, tomlFiles("/etc/kcd/kcd.toml.d")...)
	}
	files = append(files, path)
	files = append(files, tomlFiles(filepath.Join(filepath.Dir(path), "kcd.toml.d"))...)
	return files
}

func tomlFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".toml") {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(files)
	return files
}

func unknownKeyWarnings(path string, keys []toml.Key) []string {
	warnings := make([]string, 0, len(keys))
	for _, key := range keys {
		name := strings.Join(key, ".")
		warning := fmt.Sprintf("%s: unknown config key %q", path, name)
		if suggestion := nearestConfigKey(name); suggestion != "" {
			warning += fmt.Sprintf("; did you mean %q?", suggestion)
		}
		warnings = append(warnings, warning)
	}
	return warnings
}

func nearestConfigKey(name string) string {
	best := ""
	bestDistance := len(name)
	for _, candidate := range configKeyNames(reflect.TypeOf(Config{}), "") {
		distance := levenshtein(name, candidate)
		if distance < bestDistance {
			best = candidate
			bestDistance = distance
		}
	}
	if bestDistance <= 2 || bestDistance*3 <= len(name) {
		return best
	}
	return ""
}

func configKeyNames(typ reflect.Type, prefix string) []string {
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	var names []string
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := strings.Split(field.Tag.Get("toml"), ",")[0]
		if tag == "" {
			tag = strings.ToLower(field.Name)
		}
		if tag == "-" || field.Type.Kind() == reflect.Map || field.Type.Kind() == reflect.Slice {
			continue
		}
		name := tag
		if prefix != "" {
			name = prefix + "." + tag
		}
		if field.Type.Kind() == reflect.Struct {
			names = append(names, name)
			names = append(names, configKeyNames(field.Type, name)...)
		} else {
			names = append(names, name)
		}
	}
	return names
}

func levenshtein(left, right string) int {
	previous := make([]int, len(right)+1)
	for i := range previous {
		previous[i] = i
	}
	for i, leftRune := range left {
		current := make([]int, len(right)+1)
		current[0] = i + 1
		for j, rightRune := range right {
			cost := 0
			if leftRune != rightRune {
				cost = 1
			}
			current[j+1] = min(current[j]+1, previous[j+1]+1, previous[j]+cost)
		}
		previous = current
	}
	return previous[len(right)]
}

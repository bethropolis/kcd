// Package config handles loading and validating the kcd daemon configuration.
// It imports only github.com/BurntSushi/toml and internal/protocol
// (stdlib-only, so no import cycle is possible).
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/bethropolis/kcd/internal/protocol"
)

// Config holds all daemon configuration.
type Config struct {
	DeviceID    string `toml:"device_id"`
	DeviceName  string `toml:"device_name"`
	DeviceType  string `toml:"device_type"` // "desktop", "laptop", "phone", "tablet"
	CertFile    string `toml:"cert_file"`
	KeyFile     string `toml:"key_file"`
	SocketPath  string `toml:"socket_path"`
	DownloadDir string `toml:"download_dir"`
	TCPPort     int    `toml:"tcp_port"`
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
	Notifications       NotificationConfig           `toml:"notifications"`
	Battery             BatteryConfig                `toml:"battery"`
	MPRIS               MPRISConfig                  `toml:"mpris"`
	Notification        NotificationPluginConfig     `toml:"notification_plugin"`
	Share               ShareConfig                  `toml:"share"`
	SFTP                SFTPConfig                   `toml:"sftp"`
	Ping                PingConfig                   `toml:"ping"`
	Clipboard           ClipboardConfig              `toml:"clipboard"`
	Pairing             PairingConfig                `toml:"pairing"`
	Mousepad            MousepadConfig               `toml:"mousepad"`
	SMS                 SMSConfig                    `toml:"sms"`
	PruneStaleThreshold string                       `toml:"prune_stale_threshold"` // auto-remove stale unpaired devices; Go duration, "0" = disable

	ConfigPath string `toml:"-"` // populated at load time, never written to disk
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
	c.TCPPort = protocol.DefaultTCPPort
	c.LogLevel = "info"

	c.Network = NetworkConfig{DialTimeout: "5s", HandshakeTimeout: "10s", SidechannelTimeout: "15s", TransferIdleTimeout: "60s", KeepAliveIdle: "30s"}
	c.Reconnect = ReconnectConfig{InitialBackoff: "2s", MaxBackoff: "5m", FlapThreshold: "15s", SightingDriven: true, FallbackMax: "1h", StaleAfter: "24h"}
	c.Discovery = DiscoveryConfig{BroadcastInterval: "30s", BroadcastIdleInterval: "60s"}
	c.Plugins.Defaults()
	c.Commands = make(map[string]string)
	c.CommandsPerDevice = make(map[string]map[string]string)
	c.Battery.Defaults()
	c.MPRIS = MPRISConfig{PollWhilePlaying: true, PositionInterval: "2s"}
	c.Notification.Defaults()
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
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

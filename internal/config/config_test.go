package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/bethropolis/kcd/internal/protocol"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Network != (NetworkConfig{"5s", "10s", "15s", "60s", "30s"}) {
		t.Errorf("network defaults: %+v", cfg.Network)
	}
	if cfg.Reconnect != (ReconnectConfig{"2s", "5m", "15s", true, "1h", "24h"}) {
		t.Errorf("reconnect defaults: %+v", cfg.Reconnect)
	}
	if cfg.Discovery != (DiscoveryConfig{"30s", "60s"}) {
		t.Errorf("discovery defaults: %+v", cfg.Discovery)
	}
	if cfg.Pairing.IntentTTL != "5m" || cfg.Pairing.ListenTimeout != "60s" || cfg.Pairing.TimeoutSecs != 30 {
		t.Errorf("pairing defaults: %+v", cfg.Pairing)
	}
	if cfg.TCPPort != protocol.DefaultTCPPort {
		t.Errorf("tcp_port default = %d, want protocol.DefaultTCPPort (%d)", cfg.TCPPort, protocol.DefaultTCPPort)
	}
	if cfg.Share.PortMin != protocol.DefaultSidechannelPortMin || cfg.Share.PortMax != protocol.DefaultSidechannelPortMax {
		t.Errorf("share port range default = %d-%d, want %d-%d",
			cfg.Share.PortMin, cfg.Share.PortMax,
			protocol.DefaultSidechannelPortMin, protocol.DefaultSidechannelPortMax)
	}
	if cfg.Cache != (CacheConfig{}) || cfg.Ping.AppName != "" || cfg.Notifications.AppName() != "KDE Connect" {
		t.Fatal("default cache paths or notification inheritance changed")
	}
}

func loadTOML(t *testing.T, text string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kcd.toml")
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestLoadDefaults(t *testing.T) {
	for _, contents := range []string{"", "device_name = 'legacy'\n[plugins]\nping = false\n"} {
		cfg, err := loadTOML(t, contents)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Network != Defaults().Network || cfg.Pairing.IntentTTL != "5m" || cfg.ConfigPath == "" {
			t.Fatal("omitted fields did not retain defaults")
		}
	}
	path := filepath.Join(t.TempDir(), "absent.toml")
	cfg, err := Load(path)
	if err != nil || cfg.Validate() != nil {
		t.Fatalf("missing file must return valid defaults: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("Load unexpectedly created missing file")
	}
}

func TestLoadOverridesAndRoundTrip(t *testing.T) {
	cfg, err := loadTOML(t, `
[network]
dial_timeout = "750ms"
handshake_timeout = "12s"
sidechannel_timeout = "25s"
transfer_idle_timeout = "90s"
[reconnect]
initial_backoff = "3s"
max_backoff = "6m"
flap_threshold = "20s"
sighting_driven = false
fallback_max = "2h"
stale_after = "48h"
[discovery]
broadcast_interval = "45s"
broadcast_idle_interval = "90s"
[cache]
sms_attachments_dir = "/tmp/sms"
album_art_dir = "/tmp/art"
contacts_dir = "/tmp/contacts"
[pairing]
intent_ttl = "7m"
listen_timeout = "2m"
[notifications]
app_name = "My daemon"
"*" = "show"
"com.example" = "silent"
[ping]
app_name = "KDE Connect"
`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Network != (NetworkConfig{"750ms", "12s", "25s", "90s", "30s"}) || cfg.Reconnect != (ReconnectConfig{"3s", "6m", "20s", false, "2h", "48h"}) || cfg.Discovery != (DiscoveryConfig{"45s", "90s"}) {
		t.Fatal("duration overrides not decoded")
	}
	if cfg.Cache != (CacheConfig{"/tmp/sms", "/tmp/art", "/tmp/contacts"}) || cfg.Pairing.IntentTTL != "7m" || cfg.Pairing.ListenTimeout != "2m" {
		t.Fatal("cache/pairing overrides not decoded")
	}
	if cfg.Notifications.AppName() != "My daemon" || cfg.Ping.AppName != "KDE Connect" {
		t.Fatal("explicit ping override must survive, including the historical default name")
	}
	if !reflect.DeepEqual(cfg.Notifications.Filters(), NotificationConfig{"*": "show", "com.example": "silent"}) {
		t.Fatal("notification filter decode failed")
	}
	path := filepath.Join(t.TempDir(), "saved.toml")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConfigPath = path
	if !reflect.DeepEqual(cfg, reloaded) {
		t.Fatal("config changed after save/load round trip")
	}
}

func TestDurationValidation(t *testing.T) {
	fields := []struct{ section, key string }{
		{"network", "dial_timeout"}, {"network", "handshake_timeout"}, {"network", "sidechannel_timeout"},
		{"network", "keepalive_idle"},
		{"reconnect", "initial_backoff"}, {"reconnect", "max_backoff"}, {"reconnect", "flap_threshold"},
		{"reconnect", "fallback_max"}, {"reconnect", "stale_after"},
		{"discovery", "broadcast_interval"}, {"discovery", "broadcast_idle_interval"},
		{"pairing", "intent_ttl"}, {"pairing", "listen_timeout"},
		{"mpris", "position_interval"},
	}
	for _, field := range fields {
		for _, value := range []string{"", "nonsense", "10", "0", "0s", "-1s", "9999999999999999999h"} {
			t.Run(field.section+"."+field.key+"/"+value, func(t *testing.T) {
				text := "[" + field.section + "]\n" + field.key + " = '" + value + "'\n"
				if _, err := loadTOML(t, text); err == nil || !strings.Contains(err.Error(), field.section+"."+field.key) {
					t.Fatalf("Load error = %v; want field-specific validation error", err)
				}
			})
		}
	}
}

func TestDurationRelationships(t *testing.T) {
	for _, section := range []struct{ name, initial, maximum string }{
		{"reconnect", "initial_backoff", "max_backoff"},
		{"discovery", "broadcast_interval", "broadcast_idle_interval"},
	} {
		for _, maximum := range []string{"999ms", "1s", "2s"} {
			text := "[" + section.name + "]\n" + section.initial + " = '1s'\n" + section.maximum + " = '" + maximum + "'\n"
			_, err := loadTOML(t, text)
			if maximum == "999ms" {
				if err == nil || !strings.Contains(err.Error(), section.name+"."+section.maximum) {
					t.Fatalf("expected relationship error, got %v", err)
				}
			} else if err != nil {
				t.Fatalf("equal or larger maximum should be valid: %v", err)
			}
		}
	}
}

func TestFallbackMaxRelationship(t *testing.T) {
	// fallback_max must be >= max_backoff: the parked fallback may only
	// space attempts wider than the legacy loop, never tighter.
	if _, err := loadTOML(t, "[reconnect]\nfallback_max = '1m'\n"); err == nil ||
		!strings.Contains(err.Error(), "reconnect.fallback_max") {
		t.Fatalf("expected fallback_max relationship error, got %v", err)
	}
	if _, err := loadTOML(t, "[reconnect]\nmax_backoff = '5m'\nfallback_max = '1h'\n"); err != nil {
		t.Fatalf("wider fallback_max should be valid: %v", err)
	}
}

func TestKeepAliveIdleFloor(t *testing.T) {
	// Below 10s the radio never sleeps: reject, even though the value
	// parses as a positive duration.
	if _, err := loadTOML(t, "[network]\nkeepalive_idle = '5s'\n"); err == nil ||
		!strings.Contains(err.Error(), "network.keepalive_idle") {
		t.Fatalf("expected keepalive_idle floor error, got %v", err)
	}
	if _, err := loadTOML(t, "[network]\nkeepalive_idle = '120s'\n"); err != nil {
		t.Fatalf("120s keepalive_idle should be valid: %v", err)
	}
}

func TestNotificationConfig(t *testing.T) {
	for _, cfg := range []NotificationConfig{nil, {}, {"app_name": ""}, {"*": "silent"}} {
		if cfg.AppName() != "KDE Connect" {
			t.Fatal("missing or empty app_name must use default")
		}
		filters := cfg.Filters()
		if _, ok := filters["app_name"]; ok {
			t.Fatal("reserved app_name leaked into filters")
		}
		filters["new"] = "show"
		if _, ok := cfg["new"]; ok {
			t.Fatal("Filters returned shared map")
		}
	}
	cfg := NotificationConfig{"app_name": "Custom", "*": "silent", "app": "show"}
	filters := cfg.Filters()
	filters["app"] = "silent"
	cfg["*"] = "show"
	if cfg.AppName() != "Custom" || cfg["app"] != "show" || filters["*"] != "silent" {
		t.Fatal("filter map copy or app name failed")
	}
}

func TestDuration(t *testing.T) {
	for value, want := range map[string]time.Duration{"1ns": time.Nanosecond, "750ms": 750 * time.Millisecond, "1m30s": 90 * time.Second} {
		if got := Duration(value); got != want {
			t.Errorf("Duration(%q) = %v, want %v", value, got, want)
		}
	}
	t.Run("unvalidated value", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("malformed duration should expose programming error")
			}
		}()
		Duration("not-a-duration")
	})
}

func TestLoadErrors(t *testing.T) {
	for _, text := range []string{"[", "[network]\ndial_timeout = 2", "tcp_port = 0"} {
		if _, err := loadTOML(t, text); err == nil {
			t.Fatalf("expected error loading %q", text)
		}
	}
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("expected read error for directory")
	}
}

func TestExampleConfig(t *testing.T) {
	path := filepath.Join("..", "..", "packaging", "kcd.example.toml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Plugins, Defaults().Plugins) {
		t.Fatal("example changed plugin defaults")
	}
	var parsed Config
	metadata, err := toml.DecodeFile(path, &parsed)
	if err != nil {
		t.Fatal(err)
	}
	if unknown := metadata.Undecoded(); len(unknown) != 0 {
		t.Fatalf("unknown/misplaced example settings: %v", unknown)
	}
}

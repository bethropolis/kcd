package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

var exampleConfigLine = regexp.MustCompile(`^\s*#\s*((?:[A-Za-z0-9_*"-]+(?:\.[A-Za-z0-9_-]+)?\s*=\s*(?:"|'|true\b|false\b|[-0-9\[{]))|\[[A-Za-z0-9_.-]+(?:\."[^"]+")?\])`)

func TestGoldenConfigs(t *testing.T) {
	fixtures, err := filepath.Glob("testdata/golden/*.toml")
	if err != nil || len(fixtures) == 0 {
		t.Fatalf("find golden fixtures: %v", err)
	}
	for _, fixture := range fixtures {
		t.Run(filepath.Base(fixture), func(t *testing.T) {
			t.Setenv("HOME", "/golden/home")
			t.Setenv("XDG_CONFIG_HOME", "/golden/config")
			t.Setenv("XDG_RUNTIME_DIR", "/golden/runtime")
			cfg, err := Load(fixture)
			if err != nil {
				t.Fatal(err)
			}
			cfg.ConfigPath = ""
			if cfg.DeviceName != "legacy-workstation" {
				cfg.DeviceName = "5f0df2c93f64"
			}
			actual, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			snapshot := strings.TrimSuffix(fixture, ".toml") + ".json"
			if os.Getenv("UPDATE_CONFIG_GOLDEN") == "1" {
				if err := os.WriteFile(snapshot, append(actual, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if string(append(actual, '\n')) != string(want) {
				t.Fatalf("effective config differs from %s; set UPDATE_CONFIG_GOLDEN=1 only for an intentional compatibility change", snapshot)
			}
		})
	}
}

func TestExampleConfigKeysDecode(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "packaging", "kcd.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var active strings.Builder
	for _, line := range strings.Split(string(data), "\n") {
		match := exampleConfigLine.FindStringSubmatch(line)
		if len(match) != 0 {
			uncommented := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
			active.WriteString(uncommented)
			active.WriteByte('\n')
		} else if trimmed := strings.TrimSpace(line); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			active.WriteString(line)
			active.WriteByte('\n')
		}
	}
	var cfg Config
	metadata, err := toml.Decode(active.String(), &cfg)
	if err != nil {
		t.Fatalf("decode example config keys: %v", err)
	}
	if undecoded := metadata.Undecoded(); len(undecoded) != 0 {
		t.Fatalf("example config has keys not accepted by Config: %v", undecoded)
	}
}

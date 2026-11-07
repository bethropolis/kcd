package daemon

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
)

func TestProtocolCompatMatrixCoversAllCapabilities(t *testing.T) {
	docPath := filepath.Join("..", "..", "docs", "PROTOCOL_COMPAT.md")
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read PROTOCOL_COMPAT.md: %v", err)
	}
	content := string(data)

	cfg := config.Defaults()
	logger := log.NewTest(t)
	bus := events.NewBus(logger)
	reg := plugin.NewRegistry(logger)
	devices := device.NewRegistry(bus)

	setupPlugins(cfg, bus, &tls.Config{}, logger, devices, nil, func() {}, reg)
	incoming, outgoing := reg.Capabilities()

	for _, pktType := range append(incoming, outgoing...) {
		if !strings.Contains(content, pktType) {
			t.Errorf("capability %q missing from PROTOCOL_COMPAT.md", pktType)
		}
	}
}

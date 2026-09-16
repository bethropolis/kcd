package ipc

import (
	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/plugins/pair"
	"go.uber.org/zap"
	"strings"
	"testing"
	"time"
)

func TestConfiguredPairListenTimeout(t *testing.T) {
	logger := zap.NewNop()
	bus := events.NewBus(logger)
	devices := device.NewRegistry(bus)
	cfg := config.Defaults()
	pl := pair.NewPairPlugin(devices, nil, cfg.Pairing, nil, bus, logger)
	h := NewHandler(devices, nil, pl, "", bus, 0)
	h.SetPairListenTimeout(time.Millisecond)
	done := make(chan Response, 1)
	go func() { done <- h.handlePairListen() }()
	select {
	case resp := <-done:
		if resp.OK || !strings.Contains(resp.Error, "(1ms)") {
			t.Fatalf("unexpected response: %+v", resp)
		}
	case <-time.After(time.Second):
		t.Fatal("configured timeout not applied")
	}
}

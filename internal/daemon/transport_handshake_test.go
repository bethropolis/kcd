package daemon

import (
	"testing"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
)

func TestNotifyPluginsConnectedOnlyForPairedDevice(t *testing.T) {
	plugins := plugin.NewRegistry(log.Nop())
	called := 0
	plugins.Register(&stubRoutePlugin{name: "test", onConnect: func(device.Sender) { called++ }})
	dev := device.NewDevice("phone", "Phone", "phone", log.Nop())

	notifyPluginsConnected(plugins, dev)
	if called != 0 {
		t.Fatalf("OnConnect calls for unpaired device = %d, want 0", called)
	}
	dev.SetState(device.StatePaired)
	notifyPluginsConnected(plugins, dev)
	if called != 1 {
		t.Fatalf("OnConnect calls for paired device = %d, want 1", called)
	}
}

func TestNotifyPluginsDisconnectedOnlyForPairedDevice(t *testing.T) {
	plugins := plugin.NewRegistry(log.Nop())
	called := 0
	stub := &stubRoutePlugin{name: "test"}
	stub.onDisconnect = func(device.Sender) { called++ }
	plugins.Register(stub)
	dev := device.NewDevice("phone", "Phone", "phone", log.Nop())

	notifyPluginsDisconnected(plugins, dev)
	if called != 0 {
		t.Fatalf("OnDisconnect calls for unpaired device = %d, want 0", called)
	}
	dev.SetState(device.StatePaired)
	notifyPluginsDisconnected(plugins, dev)
	if called != 1 {
		t.Fatalf("OnDisconnect calls for paired device = %d, want 1", called)
	}
}

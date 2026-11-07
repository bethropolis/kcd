// Package findmyphone implements the KDE Connect Find My Phone plugin.
// It allows the client to make the paired phone ring loudly.
package findmyphone

import (
	"context"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
)

var _ plugin.Plugin = (*FindMyPhonePlugin)(nil)

// FindMyPhonePlugin sends a ring request to the paired device.
type FindMyPhonePlugin struct{}

func NewFindMyPhonePlugin() *FindMyPhonePlugin {
	return &FindMyPhonePlugin{}
}

func (p *FindMyPhonePlugin) Name() string            { return "FindMyPhone" }
func (p *FindMyPhonePlugin) Timeout() time.Duration  { return 5 * time.Second }
func (p *FindMyPhonePlugin) IncomingTypes() []string { return []string{} }
func (p *FindMyPhonePlugin) OutgoingTypes() []string {
	return []string{"kdeconnect.findmyphone.request"}
}

// Handle is a no-op — this plugin only sends packets, never receives them.
func (p *FindMyPhonePlugin) Handle(ctx context.Context, dev device.Peer, pkt *protocol.Packet) error {
	return nil
}

// Ring sends a findmyphone request to the device, making it ring loudly.
func (p *FindMyPhonePlugin) Ring(dev device.Peer) error {
	pkt, err := protocol.NewPacket("kdeconnect.findmyphone.request", map[string]interface{}{})
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

func (p *FindMyPhonePlugin) OnConnect(dev device.Peer)    {}
func (p *FindMyPhonePlugin) OnDisconnect(dev device.Peer) {}

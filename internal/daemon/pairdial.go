package daemon

import (
	"context"
	"fmt"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugins/pair"
	"github.com/bethropolis/kcd/internal/protocol"
)

// makePairDialHook creates the hook used to connect to a disconnected device
// on explicit user pair request.
func makePairDialHook(ctx context.Context, env *transportEnv, pairPlugin *pair.PairPlugin) func(deviceID string) error {
	return func(deviceID string) error {
		dev, ok := env.devices.Get(deviceID)
		if !ok {
			return fmt.Errorf("device not found")
		}
		if dev.State() == device.StatePaired {
			return fmt.Errorf("device already paired")
		}
		dev.RequestPairDial(config.Duration(env.cfg.Pairing.IntentTTL))
		ip, port := dev.DiscoveryAddr()
		if ip == nil {
			return fmt.Errorf("device address unknown yet, wait for discovery and retry")
		}
		if port == 0 {
			port = env.cfg.TCPPort
		}
		go func() {
			env.Dial(ctx, ip, port, deviceID, protocol.ProtocolVersion, true)
			if !dev.IsConnected() {
				env.logger.Warn("on-demand pair dial failed", log.String("device_id", deviceID))
				return
			}
			dev.ConsumePairDial()
			if dev.State() == device.StatePairRequestedByPeer {
				if pairPlugin != nil {
					if err := pairPlugin.AcceptPairing(dev); err != nil {
						env.logger.Warn("auto-accept on pair dial failed", log.Error(err))
					}
				}
			} else if dev.State() != device.StatePaired {
				if pairPlugin != nil {
					if _, err := pairPlugin.RequestPairing(dev); err != nil {
						env.logger.Warn("pair request on dial failed", log.Error(err))
					}
				}
			}
		}()
		return nil
	}
}

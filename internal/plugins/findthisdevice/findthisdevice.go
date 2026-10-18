package findthisdevice

import (
	"context"
	"os"
	"time"

	"github.com/bethropolis/kcd/internal/desktop/audio"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/sysexec"
)

type FindThisDevicePlugin struct {
	bus    *events.Bus
	logger log.Logger
	audio  *audio.Backend
}

func NewFindThisDevicePlugin(bus *events.Bus, logger log.Logger, backends ...*audio.Backend) *FindThisDevicePlugin {
	backend := audio.New(nil)
	if len(backends) > 0 && backends[0] != nil {
		backend = backends[0]
	}
	return &FindThisDevicePlugin{bus: bus, logger: logger, audio: backend}
}

func (p *FindThisDevicePlugin) Name() string           { return "FindThisDevice" }
func (p *FindThisDevicePlugin) Timeout() time.Duration { return 5 * time.Second }
func (p *FindThisDevicePlugin) IncomingTypes() []string {
	return []string{"kdeconnect.findmyphone.request"}
}
func (p *FindThisDevicePlugin) OutgoingTypes() []string { return []string{} }

func (p *FindThisDevicePlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	p.logger.Info("ring request received", log.String("device_id", dev.ID()))

	go func() {
		p.playAlarm()
		p.bus.Publish(events.TypeRingReceived, dev.ID(), nil)
	}()

	return nil
}

func (p *FindThisDevicePlugin) OnConnect(dev device.Sender)    {}
func (p *FindThisDevicePlugin) OnDisconnect(dev device.Sender) {}

func (p *FindThisDevicePlugin) playAlarm() {
	soundFile := p.findSoundFile()
	if soundFile == "" {
		p.logger.Warn("findthisdevice: no alarm sound file found (tried freedesktop and Oxygen paths)")
		return
	}

	restore := p.unmuteAudio()
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	players := []struct {
		cmd  string
		args []string
	}{
		{"paplay", []string{soundFile}},
		{"play", []string{soundFile}},
		{"aplay", []string{soundFile}},
	}

	for _, player := range players {
		if _, err := sysexec.Run(ctx, player.cmd, player.args...); err == nil {
			return
		}
	}

	p.logger.Warn("findthisdevice: no sound player found (tried paplay, play, aplay)")
}

func (p *FindThisDevicePlugin) findSoundFile() string {
	candidates := []string{
		"/usr/share/sounds/freedesktop/stereo/phone-incoming-call.oga",
		"/usr/share/sounds/Oxygen-Im-Phone-Ring.ogg",
		"/usr/share/sounds/freedesktop/stereo/alarm-clock-elapsed.oga",
		"/usr/share/sounds/freedesktop/stereo/complete.oga",
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func (p *FindThisDevicePlugin) unmuteAudio() func() {
	if !p.audio.Available() {
		return func() {}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, wasMuted, err := p.audio.Read(ctx)
	if err != nil {
		return func() {}
	}
	if wasMuted {
		muteCtx, muteCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer muteCancel()
		if err := p.audio.SetMuted(muteCtx, false); err != nil {
			p.logger.Warn("findthisdevice: failed to unmute audio", log.Error(err))
		}
	}
	return func() {
		if wasMuted {
			restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer restoreCancel()
			if err := p.audio.SetMuted(restoreCtx, true); err != nil {
				p.logger.Warn("findthisdevice: failed to restore audio mute", log.Error(err))
			}
		}
	}
}

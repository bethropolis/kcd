package findthisdevice

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

type FindThisDevicePlugin struct {
	bus    *events.Bus
	logger *zap.Logger
}

func NewFindThisDevicePlugin(bus *events.Bus, logger *zap.Logger) *FindThisDevicePlugin {
	return &FindThisDevicePlugin{bus: bus, logger: logger}
}

func (p *FindThisDevicePlugin) Name() string           { return "FindThisDevice" }
func (p *FindThisDevicePlugin) Timeout() time.Duration { return 5 * time.Second }
func (p *FindThisDevicePlugin) IncomingTypes() []string {
	return []string{"kdeconnect.findmyphone.request"}
}
func (p *FindThisDevicePlugin) OutgoingTypes() []string { return []string{} }

func (p *FindThisDevicePlugin) Handle(ctx context.Context, dev device.Sender, pkt *protocol.Packet) error {
	p.logger.Info("ring request received", zap.String("device_id", dev.ID()))

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
		cmd := exec.CommandContext(ctx, player.cmd, player.args...)
		if err := cmd.Run(); err == nil {
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "pactl", "get-sink-mute", "@DEFAULT_SINK@").Output()
	if err != nil {
		return func() {}
	}
	wasMuted := strings.TrimSpace(string(out)) == "Mute: yes"
	if wasMuted {
		muteCtx, muteCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer muteCancel()
		_ = exec.CommandContext(muteCtx, "pactl", "set-sink-mute", "@DEFAULT_SINK@", "0").Run()
	}
	return func() {
		if wasMuted {
			restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer restoreCancel()
			_ = exec.CommandContext(restoreCtx, "pactl", "set-sink-mute", "@DEFAULT_SINK@", "1").Run()
		}
	}
}

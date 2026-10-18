package systemvolume

import (
	"context"
	"strconv"
	"time"
)

func (p *SystemVolumePlugin) getSinks() []SinkInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	volume, muted, err := p.audio.Read(ctx)
	if err != nil {
		return nil
	}
	name := "@DEFAULT_SINK@"
	if p.audio.Name() == "wpctl" {
		name = "@DEFAULT_AUDIO_SINK@"
	}
	return []SinkInfo{{Name: name, Description: "Default Output", Volume: volume, Muted: muted, MaxVolume: 100}}
}

func (p *SystemVolumePlugin) setVolume(name string, volume int, muted bool) error {
	return p.setVolumeStr(name, strconv.Itoa(volume), muted)
}

func (p *SystemVolumePlugin) setVolumeStr(_ string, volume string, muted bool) error {
	value, err := strconv.Atoi(volume)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return p.audio.Set(ctx, value, muted)
}

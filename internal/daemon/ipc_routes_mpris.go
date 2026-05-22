package daemon

import (
	"encoding/json"
	"fmt"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/mpris"
)

func registerMprisRoutes(handler *ipc.Handler, devices *device.Registry, plugins *plugin.Registry) {
	handler.Register(ipc.CmdMprisAction, func(req ipc.Request) ipc.Response {
		var p ipc.MprisActionPayload
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return ipc.Response{OK: false, Error: "invalid payload"}
		}

		pl, ok := plugins.GetByName("MPRIS")
		if !ok {
			return ipc.Response{OK: false, Error: "mpris plugin not enabled"}
		}
		mprisPl := pl.(*mpris.MPRISPlugin)

		targetDeviceID := p.DeviceID
		if targetDeviceID == "" {
			// Pick the first connected device that has cached MPRIS state,
			// falling back to any connected device.
			devs := devices.List()
			for _, d := range devs {
				if d.IsConnected() && mprisPl.RemoteState(d.ID()) != nil {
					targetDeviceID = d.ID()
					break
				}
			}
			if targetDeviceID == "" {
				for _, d := range devs {
					if d.IsConnected() {
						targetDeviceID = d.ID()
						break
					}
				}
			}
		}
		if targetDeviceID == "" {
			return ipc.Response{OK: false, Error: "no connected device found"}
		}

		dev, ok := devices.Get(targetDeviceID)
		if !ok {
			return ipc.Response{OK: false, Error: fmt.Sprintf("device %s not found", targetDeviceID)}
		}

		// Auto-detect player from cached remote state if not specified
		player := p.Player
		if player == "" {
			if state := mprisPl.RemoteState(targetDeviceID); state != nil {
				player = state.Player
			}
		}
		if player == "" {
			return ipc.Response{OK: false, Error: "no player known for this device; specify --player"}
		}

		if err := mprisPl.SendAction(dev, player, p.Action, p.Seek, p.Volume); err != nil {
			return ipc.Response{OK: false, Error: err.Error()}
		}
		return ipc.Response{OK: true}
	})

	handler.Register(ipc.CmdMprisRemote, func(req ipc.Request) ipc.Response {
		pl, ok := plugins.GetByName("MPRIS")
		if !ok {
			return ipc.Response{OK: false, Error: "mpris plugin not enabled"}
		}
		mprisPl := pl.(*mpris.MPRISPlugin)

		// Request fresh state from all connected devices (fires requests asynchronously)
		for _, d := range devices.List() {
			if d.IsConnected() {
				_ = mprisPl.RequestState(d, "")
			}
		}

		remoteStates := mprisPl.RemoteStates()
		players := make([]ipc.MprisRemotePlayer, 0)
		for deviceID, state := range remoteStates {
			if state == nil {
				continue
			}
			players = append(players, ipc.MprisRemotePlayer{
				DeviceID:       deviceID,
				Player:         state.Player,
				Title:          state.Title,
				Artist:         state.Artist,
				Album:          state.Album,
				AlbumArtUrl:    state.AlbumArtUrl,
				Url:            state.Url,
				Length:         state.Length,
				Pos:            state.Pos,
				IsPlaying:      state.IsPlaying,
				Volume:         state.Volume,
				PlaybackStatus: state.PlaybackStatus,
				CanSeek:        state.CanSeek,
				CanGoNext:      state.CanGoNext,
				CanGoPrevious:  state.CanGoPrevious,
				CanPlay:        state.CanPlay,
				CanPause:       state.CanPause,
				CanControl:     state.CanControl,
			})
		}

		data, _ := json.Marshal(ipc.MprisRemoteResponse{Players: players})
		return ipc.Response{OK: true, Data: data}
	})
}

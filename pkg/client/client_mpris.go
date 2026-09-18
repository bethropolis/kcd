package client

import (
	"encoding/json"
	"fmt"

	"github.com/bethropolis/kcd/internal/ipc"
)

// MprisStatus returns MPRIS plugin debug information.
func (c *Client) MprisStatus() (*ipc.MprisStatusResponse, error) {
	res, err := c.Call(ipc.CmdMprisStatus, nil)
	if err != nil {
		return nil, err
	}
	var resp ipc.MprisStatusResponse
	if err := json.Unmarshal(res.Data, &resp); err != nil {
		return nil, fmt.Errorf("decode mpris status: %w", err)
	}
	return &resp, nil
}

// MprisAction sends a media control action to a remote device.
// deviceID may be empty to auto-select the first connected device.
func (c *Client) MprisAction(deviceID, player, action string) error {
	_, err := c.Call(ipc.CmdMprisAction, ipc.MprisActionPayload{
		DeviceID: deviceID,
		Player:   player,
		Action:   action,
	})
	return err
}

// MprisVolume sends a volume change to a remote device's player.
func (c *Client) MprisVolume(deviceID, player string, volume int) error {
	v := volume
	_, err := c.Call(ipc.CmdMprisAction, ipc.MprisActionPayload{
		DeviceID: deviceID,
		Player:   player,
		Volume:   &v,
	})
	return err
}

// MprisSeek sends a seek command to a remote device's player.
func (c *Client) MprisSeek(deviceID, player string, seek int64) error {
	s := seek
	_, err := c.Call(ipc.CmdMprisAction, ipc.MprisActionPayload{
		DeviceID: deviceID,
		Player:   player,
		Seek:     &s,
	})
	return err
}

// MprisRemote returns the list of remote MPRIS players with their current state.
func (c *Client) MprisRemote() (*ipc.MprisRemoteResponse, error) {
	res, err := c.Call(ipc.CmdMprisRemote, nil)
	if err != nil {
		return nil, err
	}
	var resp ipc.MprisRemoteResponse
	if err := json.Unmarshal(res.Data, &resp); err != nil {
		return nil, fmt.Errorf("decode mpris remote: %w", err)
	}
	return &resp, nil
}

// RemoteVolumeList returns the last known sink list for a device.
func (c *Client) RemoteVolumeList(deviceID string) ([]byte, error) {
	resp, err := c.Call(ipc.CmdRemoteVolumeList, ipc.DevicePayload{DeviceID: deviceID})
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// RemoteVolumeSet sets the volume for a specific sink on a remote device.
func (c *Client) RemoteVolumeSet(deviceID, sinkName string, volume int) error {
	_, err := c.Call(ipc.CmdRemoteVolumeSet, struct {
		DeviceID string `json:"deviceId"`
		Name     string `json:"name"`
		Volume   int    `json:"volume"`
	}{DeviceID: deviceID, Name: sinkName, Volume: volume})
	return err
}

// RemoteVolumeMute sets the mute state for a specific sink on a remote device.
func (c *Client) RemoteVolumeMute(deviceID, sinkName string, muted bool) error {
	_, err := c.Call(ipc.CmdRemoteVolumeMute, struct {
		DeviceID string `json:"deviceId"`
		Name     string `json:"name"`
		Muted    bool   `json:"muted"`
	}{DeviceID: deviceID, Name: sinkName, Muted: muted})
	return err
}

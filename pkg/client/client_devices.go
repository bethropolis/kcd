package client

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
)

// Connect requests the daemon to manually connect to a device by IP.
func (c *Client) Connect(ip string) error {
	_, err := c.Call(ipc.CmdConnect, ipc.ConnectPayload{IP: ip})
	return err
}

// Devices queries the daemon for all known devices.
func (c *Client) Devices() ([]device.DeviceInfo, error) {
	res, err := c.Call(ipc.CmdDevices, nil)
	if err != nil {
		return nil, err
	}

	var devices []device.DeviceInfo
	if err := json.Unmarshal(res.Data, &devices); err != nil {
		return nil, fmt.Errorf("decode devices: %w", err)
	}
	return devices, nil
}

// PairListen enters listen mode: waits for an incoming pair request, auto-accepts
// it, and returns the paired device info. Blocks up to 60 seconds.
func (c *Client) PairListen() (*ipc.PairListenResult, error) {
	// Copy the client so a long listen never changes concurrent calls' deadlines.
	listenClient := *c
	listenClient.Timeout = c.PairListenTimeout
	if listenClient.Timeout <= 0 {
		listenClient.Timeout = 70 * time.Second
	}

	resp, err := listenClient.Call(ipc.CmdPairListen, nil)
	if err != nil {
		return nil, err
	}
	var result ipc.PairListenResult
	if len(resp.Data) > 0 {
		_ = json.Unmarshal(resp.Data, &result)
	}
	return &result, nil
}

// Pair requests the daemon to pair with a specific device.
func (c *Client) Pair(deviceID string) error {
	_, err := c.Call(ipc.CmdPair, ipc.DevicePayload{DeviceID: deviceID})
	return err
}

// Unpair requests the daemon to unpair and forget a specific device.
func (c *Client) Unpair(deviceID string) error {
	_, err := c.Call(ipc.CmdUnpair, ipc.DevicePayload{DeviceID: deviceID})
	return err
}

// BroadcastStart asks the daemon to begin UDP/mDNS broadcasting.
func (c *Client) BroadcastStart() error {
	_, err := c.Call(ipc.CmdBroadcastStart, nil)
	return err
}

// BroadcastStop asks the daemon to stop UDP/mDNS broadcasting.
func (c *Client) BroadcastStop() error {
	_, err := c.Call(ipc.CmdBroadcastStop, nil)
	return err
}

// Status returns runtime status information from the daemon.
func (c *Client) Status() (*ipc.StatusResponse, error) {
	res, err := c.Call(ipc.CmdStatus, nil)
	if err != nil {
		return nil, err
	}
	var resp ipc.StatusResponse
	if err := json.Unmarshal(res.Data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Connectivity returns the last raw connectivity report for a device.
// Callers decode it (same shape as connectivity.update event payloads).
func (c *Client) Connectivity(deviceID string) (json.RawMessage, error) {
	res, err := c.Call(ipc.CmdConnectivity, ipc.DevicePayload{DeviceID: deviceID})
	if err != nil {
		return nil, err
	}
	return res.Data, nil
}

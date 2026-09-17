package client

import (
	"encoding/json"

	"github.com/bethropolis/kcd/internal/ipc"
)

// Ping sends a ping packet to a specific device.
func (c *Client) Ping(deviceID string) error {
	_, err := c.Call(ipc.CmdPing, ipc.DevicePayload{DeviceID: deviceID})
	return err
}

// Battery queries the daemon for a device's battery state.
func (c *Client) Battery(deviceID string) (int, bool, error) {
	res, err := c.Call(ipc.CmdBattery, ipc.DevicePayload{DeviceID: deviceID})
	if err != nil {
		return 0, false, err
	}

	var data struct {
		Charge   int  `json:"charge"`
		Charging bool `json:"charging"`
	}
	if err := json.Unmarshal(res.Data, &data); err != nil {
		return 0, false, err
	}
	return data.Charge, data.Charging, nil
}

// ClipboardPush triggers an outgoing clipboard sync from desktop to device.
func (c *Client) ClipboardPush(deviceID string) error {
	_, err := c.Call(ipc.CmdClipboardPush, ipc.DevicePayload{DeviceID: deviceID})
	return err
}

// RunList requests the remote device to send its command list.
func (c *Client) RunList(deviceID string) error {
	_, err := c.Call(ipc.CmdRunList, ipc.DevicePayload{DeviceID: deviceID})
	return err
}

// RunExec requests the remote device to execute a specific command key.
func (c *Client) RunExec(deviceID string, key string) error {
	_, err := c.Call(ipc.CmdRunExec, ipc.DevicePayload{DeviceID: deviceID, Key: key})
	return err
}

// ShareFile requests the daemon to send a local file to the remote device.
func (c *Client) ShareFile(deviceID string, filePath string) error {
	_, err := c.Call(ipc.CmdShare, ipc.SharePayload{DeviceID: deviceID, FilePath: filePath})
	return err
}

// NotifyReply requests the daemon to send a reply to an Android notification.
func (c *Client) NotifyReply(deviceID, replyID, message string) error {
	_, err := c.Call(ipc.CmdNotifyReply, ipc.NotifyReplyPayload{
		DeviceID: deviceID,
		ReplyID:  replyID,
		Message:  message,
	})
	return err
}

// CallMute requests the daemon to mute an incoming call on the remote device.
func (c *Client) CallMute(deviceID string) error {
	_, err := c.Call(ipc.CmdCallMute, ipc.DevicePayload{DeviceID: deviceID})
	return err
}

// FindMyPhone requests the remote device to ring loudly.
func (c *Client) FindMyPhone(deviceID string) error {
	_, err := c.Call(ipc.CmdFindMyPhone, ipc.DevicePayload{DeviceID: deviceID})
	return err
}

// Lock requests the daemon to lock the session.
func (c *Client) Lock(deviceID string) error {
	_, err := c.Call(ipc.CmdLock, ipc.DevicePayload{DeviceID: deviceID})
	return err
}

// Unlock requests the daemon to unlock the session.
func (c *Client) Unlock(deviceID string) error {
	_, err := c.Call(ipc.CmdUnlock, ipc.DevicePayload{DeviceID: deviceID})
	return err
}

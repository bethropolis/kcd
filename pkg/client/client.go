// Package client implements the local IPC client for kcdctl.
package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/ipc"
)

// Client connects to the kcd daemon via Unix socket.
type Client struct {
	SocketPath string
	Timeout    time.Duration
}

// Call dialed the daemon, sends a request, and returns the response.
func (c *Client) Call(cmd string, payload interface{}) (*ipc.Response, error) {
	conn, err := net.Dial("unix", c.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("kcd daemon not running or socket error: %w", err)
	}
	defer conn.Close()

	if c.Timeout > 0 {
		conn.SetDeadline(time.Now().Add(c.Timeout))
	} else {
		conn.SetDeadline(time.Now().Add(5 * time.Second))
	}

	var rawPayload []byte
	if payload != nil {
		rawPayload, err = json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal payload: %w", err)
		}
	}

	req := ipc.Request{
		Command: cmd,
		Payload: rawPayload,
	}

	reqBytes, _ := json.Marshal(req)
	reqBytes = append(reqBytes, '\n')

	if _, err := conn.Write(reqBytes); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	resBytes, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var res ipc.Response
	if err := json.Unmarshal(resBytes, &res); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if !res.OK {
		return nil, fmt.Errorf("daemon error: %s", res.Error)
	}

	return &res, nil
}

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
	// Use a longer timeout for the listen operation
	savedTimeout := c.Timeout
	c.Timeout = 70 * time.Second
	defer func() { c.Timeout = savedTimeout }()

	resp, err := c.Call(ipc.CmdPairListen, nil)
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

// SftpMount requests the daemon to initiate an SFTP connection to the remote device.
func (c *Client) SftpMount(deviceID string) error {
	_, err := c.Call(ipc.CmdSftpMount, ipc.DevicePayload{DeviceID: deviceID})
	return err
}

// SftpInfo returns the cached SFTP connection details for a device.
func (c *Client) SftpInfo(deviceID string) (*ipc.SftpInfoResponse, error) {
	resp, err := c.Call(ipc.CmdSftpInfo, ipc.DevicePayload{DeviceID: deviceID})
	if err != nil {
		return nil, err
	}
	var info ipc.SftpInfoResponse
	if len(resp.Data) > 0 {
		_ = json.Unmarshal(resp.Data, &info)
	}
	return &info, nil
}

// SftpVolumes returns the list of available storage volumes from a device.
func (c *Client) SftpVolumes(deviceID string) ([]ipc.StorageVolumeResponse, error) {
	resp, err := c.Call(ipc.CmdSftpVolumes, ipc.DevicePayload{DeviceID: deviceID})
	if err != nil {
		return nil, err
	}
	var volumes []ipc.StorageVolumeResponse
	if len(resp.Data) > 0 {
		_ = json.Unmarshal(resp.Data, &volumes)
	}
	return volumes, nil
}

// SftpMountLocal requests the daemon to request SFTP credentials from the
// phone, wait for the response, mount via sshfs, and open the result in
// the default file manager. Returns the local browse path on success.
func (c *Client) SftpMountLocal(deviceID string) (string, error) {
	resp, err := c.Call(ipc.CmdSftpMountLocal, ipc.DevicePayload{DeviceID: deviceID})
	if err != nil {
		return "", err
	}
	var result struct {
		Path string `json:"path"`
	}
	if len(resp.Data) > 0 {
		_ = json.Unmarshal(resp.Data, &result)
	}
	return result.Path, nil
}

// SftpUnmount cleanly unmounts a previously mounted phone filesystem.
func (c *Client) SftpUnmount(deviceID string) error {
	_, err := c.Call(ipc.CmdSftpUnmount, ipc.DevicePayload{DeviceID: deviceID})
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

// SendSMS requests the remote device to send an SMS.
func (c *Client) SendSMS(deviceID, phoneNumber, message string) error {
	_, err := c.Call(ipc.CmdSendSMS, ipc.SMSPayload{
		DeviceID:    deviceID,
		PhoneNumber: phoneNumber,
		Message:     message,
	})
	return err
}

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

// WatchFile subscribes to daemon events and streams them to the given channel.
func (c *Client) Watch(ctx context.Context, filter []string, ch chan<- events.Event) error {
	conn, err := net.Dial("unix", c.SocketPath)
	if err != nil {
		return fmt.Errorf("kcd daemon not running or socket error: %w", err)
	}

	payload, _ := json.Marshal(ipc.WatchPayload{Events: filter})
	req := ipc.Request{
		Command: ipc.CmdWatch,
		Payload: payload,
	}

	reqBytes, _ := json.Marshal(req)
	reqBytes = append(reqBytes, '\n')

	if _, err := conn.Write(reqBytes); err != nil {
		conn.Close()
		return fmt.Errorf("write request: %w", err)
	}

	reader := bufio.NewReader(conn)
	resBytes, err := reader.ReadBytes('\n')
	if err != nil {
		conn.Close()
		return fmt.Errorf("read response: %w", err)
	}

	var res ipc.Response
	if err := json.Unmarshal(resBytes, &res); err != nil {
		conn.Close()
		return fmt.Errorf("unmarshal response: %w", err)
	}

	if !res.OK {
		conn.Close()
		return fmt.Errorf("daemon error: %s", res.Error)
	}

	defer conn.Close()

	// Create a goroutine to close the connection if context is canceled
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return fmt.Errorf("stream read error: %w", err)
		}

		var ev events.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		select {
		case ch <- ev:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

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

// SftpMountLocal requests the daemon to physically mount the remote device via sshfs.
// It returns the local path where the device is mounted.
func (c *Client) SftpMountLocal(deviceID string) (string, error) {
	res, err := c.Call(ipc.CmdSftpMountLocal, ipc.DevicePayload{DeviceID: deviceID})
	if err != nil {
		return "", err
	}
	var data struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(res.Data, &data); err != nil {
		return "", fmt.Errorf("decode path: %w", err)
	}
	return data.Path, nil
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

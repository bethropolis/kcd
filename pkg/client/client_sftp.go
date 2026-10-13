package client

import (
	"time"

	"encoding/json"

	"github.com/bethropolis/kcd/internal/ipc"
)

// SftpMount requests the daemon to initiate an SFTP connection. readOnly is
// tri-state (true/false/nil for default) so absent never forces writable.
func (c *Client) SftpMount(deviceID string, readOnly *bool) error {
	_, err := c.Call(ipc.CmdSftpMount, ipc.SftpMountPayload{DeviceID: deviceID, ReadOnly: readOnly})
	return err
}

// SftpInfo returns cached SFTP details; the live password only when
// showPassword is true, otherwise the daemon omits it.
func (c *Client) SftpInfo(deviceID string, showPassword bool) (*ipc.SftpInfoResponse, error) {
	resp, err := c.Call(ipc.CmdSftpInfo, ipc.SftpInfoPayload{DeviceID: deviceID, ShowPassword: showPassword})
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

// SftpMountLocal fetches credentials, mounts via sshfs, and opens the result
// in the file manager. Returns the local browse path on success.
func (c *Client) SftpMountLocal(deviceID string, readOnly *bool) (string, error) {
	resp, err := c.callWithTimeout(ipc.CmdSftpMountLocal, ipc.SftpMountPayload{DeviceID: deviceID, ReadOnly: readOnly}, c.sftpTimeout())
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

// SftpBrowse lists volumes (empty volume) or mounts one by index, name, or
// path. Returns the mount path, or the volume list when only listing.
func (c *Client) SftpBrowse(deviceID string, volume string, readOnly *bool) (string, []ipc.StorageVolumeResponse, error) {
	resp, err := c.callWithTimeout(ipc.CmdSftpBrowse, ipc.SftpBrowsePayload{
		DeviceID: deviceID,
		Volume:   volume,
		ReadOnly: readOnly,
	}, c.sftpTimeout())
	if err != nil {
		return "", nil, err
	}
	var result ipc.SftpBrowseResponse
	if len(resp.Data) > 0 {
		_ = json.Unmarshal(resp.Data, &result)
	}
	return result.Path, result.Volumes, nil
}

// sftpTimeout is the deadline for calls that wait on the phone.
func (c *Client) sftpTimeout() time.Duration {
	if c.SftpTimeout > 0 {
		return c.SftpTimeout
	}
	return 60 * time.Second
}

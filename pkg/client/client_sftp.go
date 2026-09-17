package client

import (
	"encoding/json"

	"github.com/bethropolis/kcd/internal/ipc"
)

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

// SftpBrowse requests fresh SFTP credentials from the phone and either lists
// available volumes (volume arg empty) or mounts the specified volume.
// volume can be an index (0-based), volume name, or path.
// Returns the mount path (empty if listing) and available volumes.
func (c *Client) SftpBrowse(deviceID string, volume string) (string, []ipc.StorageVolumeResponse, error) {
	resp, err := c.Call(ipc.CmdSftpBrowse, ipc.SftpBrowsePayload{
		DeviceID: deviceID,
		Volume:   volume,
	})
	if err != nil {
		return "", nil, err
	}
	var result ipc.SftpBrowseResponse
	if len(resp.Data) > 0 {
		_ = json.Unmarshal(resp.Data, &result)
	}
	return result.Path, result.Volumes, nil
}

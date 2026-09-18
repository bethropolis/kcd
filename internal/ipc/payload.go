package ipc

// GetDeviceID methods let route helpers treat every device-addressed
// payload uniformly. Each method is a one-line accessor over the
// payload's deviceId field; the JSON shapes are unchanged.
func (p DevicePayload) GetDeviceID() string        { return p.DeviceID }
func (p SharePayload) GetDeviceID() string         { return p.DeviceID }
func (p NotifyReplyPayload) GetDeviceID() string   { return p.DeviceID }
func (p NotifyDismissPayload) GetDeviceID() string { return p.DeviceID }
func (p SMSPayload) GetDeviceID() string           { return p.DeviceID }
func (p SMSConvPayload) GetDeviceID() string       { return p.DeviceID }
func (p SMSAttachmentPayload) GetDeviceID() string { return p.DeviceID }
func (p SftpBrowsePayload) GetDeviceID() string    { return p.DeviceID }
func (p MprisActionPayload) GetDeviceID() string   { return p.DeviceID }

// RemoteVolumeSetPayload is used for CmdRemoteVolumeSet.
type RemoteVolumeSetPayload struct {
	DeviceID string `json:"deviceId"`
	Name     string `json:"name"`
	Volume   int    `json:"volume"`
}

// GetDeviceID returns the addressed device.
func (p RemoteVolumeSetPayload) GetDeviceID() string { return p.DeviceID }

// RemoteVolumeMutePayload is used for CmdRemoteVolumeMute.
type RemoteVolumeMutePayload struct {
	DeviceID string `json:"deviceId"`
	Name     string `json:"name"`
	Muted    bool   `json:"muted"`
}

// GetDeviceID returns the addressed device.
func (p RemoteVolumeMutePayload) GetDeviceID() string { return p.DeviceID }

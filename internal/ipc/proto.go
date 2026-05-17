// Package ipc defines the local Unix socket protocol used between the daemon
// and the command line interface.
package ipc

import "encoding/json"

// Supported IPC command constants.
const (
	CmdDevices        = "devices"
	CmdPair           = "pair"
	CmdPairListen     = "pair_listen"
	CmdUnpair         = "unpair"
	CmdPing           = "ping"
	CmdBattery        = "battery"
	CmdClipboardPush  = "clipboard_push"
	CmdRunList        = "run_list"
	CmdRunExec        = "run_exec"
	CmdShare          = "share"
	CmdConnect        = "connect"
	CmdWatch          = "watch"
	CmdSftpMount      = "sftp_mount"
	CmdSftpInfo       = "sftp_info"
	CmdSftpVolumes    = "sftp_volumes"
	CmdNotifyReply    = "notify_reply"
	CmdCallMute       = "call_mute"
	CmdFindMyPhone    = "findmyphone"
	CmdLock           = "lock"
	CmdUnlock         = "unlock"
	CmdSendSMS        = "send_sms"
	CmdSftpMountLocal = "sftp_mount_local"
	CmdSftpUnmount    = "sftp_unmount"
	CmdStatus         = "status"
	CmdMprisStatus    = "mpris_status"
)

// ConnectPayload carries the target IP for the CmdConnect command.
type ConnectPayload struct {
	IP string `json:"ip"`
}

// Request is sent from the client to the local daemon.
type Request struct {
	Command string          `json:"cmd"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// WatchPayload is used for CmdWatch to configure event streaming.
type WatchPayload struct {
	Events []string `json:"events,omitempty"`
}

// Response is sent from the daemon back to the client.
type Response struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// PairListenResult is returned by CmdPairListen on success.
type PairListenResult struct {
	DeviceID        string `json:"deviceId"`
	DeviceName      string `json:"deviceName"`
	VerificationKey string `json:"verificationKey,omitempty"`
}

// DevicePayload is sent in requests requiring a device ID (like pair/unpair/ping).
type DevicePayload struct {
	DeviceID string `json:"deviceId"`
	Key      string `json:"key,omitempty"`
}

// SharePayload is used for CmdShare.
type SharePayload struct {
	DeviceID string `json:"deviceId"`
	FilePath string `json:"filePath"`
}

// NotifyReplyPayload is used for CmdNotifyReply.
type NotifyReplyPayload struct {
	DeviceID string `json:"deviceId"`
	ReplyID  string `json:"replyId"`
	Message  string `json:"message"`
}

// SMSPayload is used for CmdSendSMS.
type SMSPayload struct {
	DeviceID    string `json:"deviceId"`
	PhoneNumber string `json:"phoneNumber"`
	Message     string `json:"message"`
}

// StatusResponse is returned by CmdStatus.
type StatusResponse struct {
	Version        string   `json:"version"`
	StartedAt      string   `json:"startedAt"`
	UptimeHuman    string   `json:"uptimeHuman"`
	SocketPath     string   `json:"socketPath"`
	ConfigPath     string   `json:"configPath"`
	Plugins        []string `json:"plugins"`
	DeviceCount    int      `json:"deviceCount"`
	ConnectedCount int      `json:"connectedCount"`
}

// SftpInfoResponse carries cached SFTP connection details returned by CmdSftpInfo.
type SftpInfoResponse struct {
	IP       string                  `json:"ip"`
	Port     json.Number             `json:"port"`
	User     string                  `json:"user"`
	Password string                  `json:"password"`
	Path     string                  `json:"path"`
	Volumes  []StorageVolumeResponse `json:"volumes,omitempty"`
}

// StorageVolumeResponse describes a single browsable storage root on a device.
type StorageVolumeResponse struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type MprisPlayerInfo struct {
	DisplayName    string `json:"displayName"`
	BusName        string `json:"busName"`
	ShortName      string `json:"shortName"`
	Title          string `json:"title"`
	Artist         string `json:"artist"`
	Album          string `json:"album"`
	PlaybackStatus string `json:"playbackStatus"`
	IsPlaying      bool   `json:"isPlaying"`
	Volume         int    `json:"volume"`
	Pos            int64  `json:"pos"`
	Length         int64  `json:"length"`
	AlbumArtUrl    string `json:"albumArtUrl"`
	CanSeek        bool   `json:"canSeek"`
	CanGoNext      bool   `json:"canGoNext"`
	CanGoPrevious  bool   `json:"canGoPrevious"`
	CanPlay        bool   `json:"canPlay"`
	CanPause       bool   `json:"canPause"`
	Error          string `json:"error,omitempty"`
}

type MprisStatusResponse struct {
	WatcherRunning bool              `json:"watcherRunning"`
	DeviceCount    int               `json:"deviceCount"`
	Players        []MprisPlayerInfo `json:"players"`
	PlayerMappings map[string]string `json:"playerMappings"`
}

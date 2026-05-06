// Package ipc defines the local Unix socket protocol used between the daemon
// and the command line interface.
package ipc

import "encoding/json"

// Supported IPC command constants.
const (
	CmdDevices        = "devices"
	CmdPair           = "pair"
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
	CmdNotifyReply    = "notify_reply"
	CmdCallMute       = "call_mute"
	CmdFindMyPhone    = "findmyphone"
	CmdLock           = "lock"
	CmdUnlock         = "unlock"
	CmdSendSMS        = "send_sms"
	CmdSftpMountLocal = "sftp_mount_local"
	CmdSftpUnmount    = "sftp_unmount"
	CmdStatus         = "status"
)

// Request is sent from the client to the local daemon.
type ConnectPayload struct {
	IP string `json:"ip"`
}

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

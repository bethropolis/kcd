package client

import "errors"

var (
	ErrInvalidPayload     = errors.New("invalid payload")
	ErrDeviceNotFound     = errors.New("device not found")
	ErrDeviceNotConnected = errors.New("device not connected")
	ErrPluginDisabled     = errors.New("plugin disabled")
	ErrTimeout            = errors.New("timeout")
)

// Error is a stable daemon error returned by an IPC request.
type Error struct {
	Code    string
	Message string
	Details []byte
}

func (e *Error) Error() string { return e.Message }

func (e *Error) Is(target error) bool {
	known := map[string]error{
		"invalid_payload":      eInvalidPayload,
		"device_not_found":     eDeviceNotFound,
		"device_not_connected": eDeviceNotConnected,
		"plugin_disabled":      ePluginDisabled,
		"timeout":              eTimeout,
	}
	return known[e.Code] == target
}

var (
	eInvalidPayload     = ErrInvalidPayload
	eDeviceNotFound     = ErrDeviceNotFound
	eDeviceNotConnected = ErrDeviceNotConnected
	ePluginDisabled     = ErrPluginDisabled
	eTimeout            = ErrTimeout
)

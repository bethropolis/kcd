package client

import "errors"

var (
	ErrInvalidPayload          = errors.New("invalid payload")
	ErrDeviceNotFound          = errors.New("device not found")
	ErrDeviceNotConnected      = errors.New("device not connected")
	ErrDeviceNotPaired         = errors.New("device not paired")
	ErrPluginDisabled          = errors.New("plugin disabled")
	ErrPluginDisabledForDevice = errors.New("plugin disabled for device")
	ErrTimeout                 = errors.New("timeout")
	ErrBusy                    = errors.New("busy")
	ErrNotSupported            = errors.New("not supported")
	ErrPermissionDenied        = errors.New("permission denied")
	ErrInternal                = errors.New("internal error")
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
		"invalid_payload":            eInvalidPayload,
		"device_not_found":           eDeviceNotFound,
		"device_not_connected":       eDeviceNotConnected,
		"device_not_paired":          eDeviceNotPaired,
		"plugin_disabled":            ePluginDisabled,
		"plugin_disabled_for_device": ePluginDisabledForDevice,
		"timeout":                    eTimeout,
		"busy":                       eBusy,
		"not_supported":              eNotSupported,
		"permission_denied":          ePermissionDenied,
		"internal":                   eInternal,
	}
	return known[e.Code] == target
}

var (
	eInvalidPayload          = ErrInvalidPayload
	eDeviceNotFound          = ErrDeviceNotFound
	eDeviceNotConnected      = ErrDeviceNotConnected
	eDeviceNotPaired         = ErrDeviceNotPaired
	ePluginDisabled          = ErrPluginDisabled
	ePluginDisabledForDevice = ErrPluginDisabledForDevice
	eTimeout                 = ErrTimeout
	eBusy                    = ErrBusy
	eNotSupported            = ErrNotSupported
	ePermissionDenied        = ErrPermissionDenied
	eInternal                = ErrInternal
)

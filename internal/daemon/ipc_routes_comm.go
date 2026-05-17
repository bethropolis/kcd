package daemon

import (
	"encoding/json"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/plugins/notification"
	"github.com/bethropolis/kcd/internal/plugins/sms"
	"github.com/bethropolis/kcd/internal/plugins/telephony"
)

func registerCommRoutes(handler *ipc.Handler, cfg *config.Config, devices *device.Registry, plugins *plugin.Registry) {
	if cfg.Plugins.SMS {
		handler.Register(ipc.CmdSendSMS, func(req ipc.Request) ipc.Response {
			var p ipc.SMSPayload
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				return ipc.Response{OK: false, Error: "invalid payload"}
			}
			pl, ok := plugins.GetByName("SMS")
			if !ok {
				return ipc.Response{OK: false, Error: "sms plugin not enabled"}
			}
			dev, ok := devices.Get(p.DeviceID)
			if !ok {
				return ipc.Response{OK: false, Error: "device not found"}
			}
			if err := pl.(*sms.SMSPlugin).SendSMS(dev, p.PhoneNumber, p.Message); err != nil {
				return ipc.Response{OK: false, Error: err.Error()}
			}
			return ipc.Response{OK: true}
		})
	}
	if cfg.Plugins.Telephony {
		handler.Register(ipc.CmdCallMute, func(req ipc.Request) ipc.Response {
			var p ipc.DevicePayload
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				return ipc.Response{OK: false, Error: "invalid payload"}
			}
			pl, ok := plugins.GetByName("Telephony")
			if !ok {
				return ipc.Response{OK: false, Error: "telephony plugin not enabled"}
			}
			dev, ok := devices.Get(p.DeviceID)
			if !ok {
				return ipc.Response{OK: false, Error: "device not found"}
			}
			if err := pl.(*telephony.TelephonyPlugin).Mute(dev); err != nil {
				return ipc.Response{OK: false, Error: err.Error()}
			}
			return ipc.Response{OK: true}
		})
	}
	if cfg.Plugins.Notification {
		handler.Register(ipc.CmdNotifyReply, func(req ipc.Request) ipc.Response {
			var p ipc.NotifyReplyPayload
			if err := json.Unmarshal(req.Payload, &p); err != nil {
				return ipc.Response{OK: false, Error: "invalid payload"}
			}
			pl, ok := plugins.GetByName("Notification")
			if !ok {
				return ipc.Response{OK: false, Error: "notification plugin not enabled"}
			}
			dev, ok := devices.Get(p.DeviceID)
			if !ok {
				return ipc.Response{OK: false, Error: "device not found"}
			}
			if err := pl.(*notification.NotificationPlugin).RequestReply(dev, p.ReplyID, p.Message); err != nil {
				return ipc.Response{OK: false, Error: err.Error()}
			}
			return ipc.Response{OK: true}
		})
	}
}

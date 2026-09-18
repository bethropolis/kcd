package daemon

import (
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
			return deviceRoute(req, &p, devices, plugins, "SMS", func(dev *device.Device, pl plugin.Plugin) ipc.Response {
				if err := pl.(*sms.SMSPlugin).SendSMS(dev, p.PhoneNumber, p.Message); err != nil {
					return ipc.Response{OK: false, Error: err.Error()}
				}
				return ipc.Response{OK: true}
			})
		})

		handler.Register(ipc.CmdSmsRequestConvs, func(req ipc.Request) ipc.Response {
			var p ipc.DevicePayload
			return deviceRoute(req, &p, devices, plugins, "SMS", func(dev *device.Device, pl plugin.Plugin) ipc.Response {
				if err := pl.(*sms.SMSPlugin).RequestConversations(dev); err != nil {
					return ipc.Response{OK: false, Error: err.Error()}
				}
				return ipc.Response{OK: true}
			})
		})

		handler.Register(ipc.CmdSmsRequestConv, func(req ipc.Request) ipc.Response {
			var p ipc.SMSConvPayload
			return deviceRoute(req, &p, devices, plugins, "SMS", func(dev *device.Device, pl plugin.Plugin) ipc.Response {
				if err := pl.(*sms.SMSPlugin).RequestConversation(dev, p.ThreadID, p.RangeStartTimestamp, p.NumberToRequest); err != nil {
					return ipc.Response{OK: false, Error: err.Error()}
				}
				return ipc.Response{OK: true}
			})
		})

		handler.Register(ipc.CmdSmsRequestAttachment, func(req ipc.Request) ipc.Response {
			var p ipc.SMSAttachmentPayload
			return deviceRoute(req, &p, devices, plugins, "SMS", func(dev *device.Device, pl plugin.Plugin) ipc.Response {
				if err := pl.(*sms.SMSPlugin).RequestAttachment(dev, p.PartID, p.UniqueIdentifier); err != nil {
					return ipc.Response{OK: false, Error: err.Error()}
				}
				return ipc.Response{OK: true}
			})
		})
	}
	if cfg.Plugins.Telephony {
		handler.Register(ipc.CmdCallMute, func(req ipc.Request) ipc.Response {
			var p ipc.DevicePayload
			return deviceRoute(req, &p, devices, plugins, "Telephony", func(dev *device.Device, pl plugin.Plugin) ipc.Response {
				if err := pl.(*telephony.TelephonyPlugin).Mute(dev); err != nil {
					return ipc.Response{OK: false, Error: err.Error()}
				}
				return ipc.Response{OK: true}
			})
		})
	}
	if cfg.Plugins.Notification {
		handler.Register(ipc.CmdNotifyReply, func(req ipc.Request) ipc.Response {
			var p ipc.NotifyReplyPayload
			return deviceRoute(req, &p, devices, plugins, "Notification", func(dev *device.Device, pl plugin.Plugin) ipc.Response {
				if err := pl.(*notification.NotificationPlugin).RequestReply(dev, p.ReplyID, p.Message); err != nil {
					return ipc.Response{OK: false, Error: err.Error()}
				}
				return ipc.Response{OK: true}
			})
		})
		handler.Register(ipc.CmdNotifyDismiss, func(req ipc.Request) ipc.Response {
			var p ipc.NotifyDismissPayload
			return deviceRoute(req, &p, devices, plugins, "Notification", func(dev *device.Device, pl plugin.Plugin) ipc.Response {
				if err := pl.(*notification.NotificationPlugin).Dismiss(dev, p.NotificationID); err != nil {
					return ipc.Response{OK: false, Error: err.Error()}
				}
				return ipc.Response{OK: true}
			})
		})
	}
}

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
			return deviceRoute(req, &p, devices, plugins, "SMS", func(dev *device.Device, smsPl *sms.SMSPlugin) ipc.Response {
				var subID []int64
				if p.SubID != nil {
					subID = append(subID, *p.SubID)
				}
				if err := smsPl.SendSMS(dev, p.PhoneNumber, p.Message, subID...); err != nil {
					return ipc.Response{OK: false, Error: err.Error()}
				}
				return ipc.Response{OK: true}
			})
		})

		handler.Register(ipc.CmdSmsRequestConvs, func(req ipc.Request) ipc.Response {
			var p ipc.DevicePayload
			return deviceRoute(req, &p, devices, plugins, "SMS", func(dev *device.Device, smsPl *sms.SMSPlugin) ipc.Response {
				if err := smsPl.RequestConversations(dev); err != nil {
					return ipc.Response{OK: false, Error: err.Error()}
				}
				return ipc.Response{OK: true}
			})
		})

		handler.Register(ipc.CmdSmsRequestConv, func(req ipc.Request) ipc.Response {
			var p ipc.SMSConvPayload
			return deviceRoute(req, &p, devices, plugins, "SMS", func(dev *device.Device, smsPl *sms.SMSPlugin) ipc.Response {
				rangeStart, count := int64(-1), int64(-1)
				if p.RangeStartTimestamp != nil {
					rangeStart = *p.RangeStartTimestamp
				}
				if p.NumberToRequest != nil {
					count = *p.NumberToRequest
				}
				if err := smsPl.RequestConversation(dev, p.ThreadID, rangeStart, count); err != nil {
					return ipc.Response{OK: false, Error: err.Error()}
				}
				return ipc.Response{OK: true}
			})
		})

		handler.Register(ipc.CmdSmsRequestAttachment, func(req ipc.Request) ipc.Response {
			var p ipc.SMSAttachmentPayload
			return deviceRoute(req, &p, devices, plugins, "SMS", func(dev *device.Device, smsPl *sms.SMSPlugin) ipc.Response {
				var threadID []int64
				if p.ThreadID != nil {
					threadID = append(threadID, *p.ThreadID)
				}
				if err := smsPl.RequestAttachment(dev, p.PartID, p.UniqueIdentifier, threadID...); err != nil {
					return ipc.Response{OK: false, Error: err.Error()}
				}
				return ipc.Response{OK: true}
			})
		})
	}
	if cfg.Plugins.Telephony {
		handler.Register(ipc.CmdCallMute, func(req ipc.Request) ipc.Response {
			var p ipc.DevicePayload
			return deviceRoute(req, &p, devices, plugins, "Telephony", func(dev *device.Device, telPl *telephony.TelephonyPlugin) ipc.Response {
				if err := telPl.Mute(dev); err != nil {
					return ipc.Response{OK: false, Error: err.Error()}
				}
				return ipc.Response{OK: true}
			})
		})
	}
	if cfg.Plugins.Notification {
		handler.Register(ipc.CmdNotifyReply, func(req ipc.Request) ipc.Response {
			var p ipc.NotifyReplyPayload
			return deviceRoute(req, &p, devices, plugins, "Notification", func(dev *device.Device, notifPl *notification.NotificationPlugin) ipc.Response {
				if err := notifPl.RequestReply(dev, p.ReplyID, p.Message); err != nil {
					return ipc.Response{OK: false, Error: err.Error()}
				}
				return ipc.Response{OK: true}
			})
		})
		handler.Register(ipc.CmdNotifyDismiss, func(req ipc.Request) ipc.Response {
			var p ipc.NotifyDismissPayload
			return deviceRoute(req, &p, devices, plugins, "Notification", func(dev *device.Device, notifPl *notification.NotificationPlugin) ipc.Response {
				if err := notifPl.Dismiss(dev, p.NotificationID); err != nil {
					return ipc.Response{OK: false, Error: err.Error()}
				}
				return ipc.Response{OK: true}
			})
		})
		handler.Register(ipc.CmdNotifyAction, func(req ipc.Request) ipc.Response {
			var p ipc.NotifyActionPayload
			return deviceRoute(req, &p, devices, plugins, "Notification", func(dev *device.Device, notifPl *notification.NotificationPlugin) ipc.Response {
				notifID := p.Key
				if notifID == "" {
					notifID = p.NotificationID
				}
				if notifID == "" {
					return ipc.Response{OK: false, Error: "missing notification id"}
				}
				if p.Action == "" {
					return ipc.Response{OK: false, Error: "missing action"}
				}
				if err := notifPl.TriggerAction(dev, notifID, p.Action); err != nil {
					return ipc.Response{OK: false, Error: err.Error()}
				}
				return ipc.Response{OK: true}
			})
		})
	}
}

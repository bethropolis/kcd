package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/discovery"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/ipc"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugins/mpris"
	"github.com/bethropolis/kcd/internal/protocol"
)

func eventNames(types []events.EventType) []string {
	names := make([]string, len(types))
	for i, typ := range types {
		names[i] = string(typ)
	}
	return names
}

func registerIPCRoutes(handler *ipc.Handler, env *transportEnv, bc *discovery.BroadcasterController, ctx context.Context, startedAt time.Time) {
	cfg := env.cfg
	devices := env.devices
	plugins := env.plugins
	logger := env.logger
	handler.Register(ipc.CmdConfigGet, func(req ipc.Request) ipc.Response {
		var payload ipc.ConfigGetPayload
		if len(req.Payload) > 0 {
			if err := json.Unmarshal(req.Payload, &payload); err != nil {
				return ipc.Err(ipc.CodeInvalidPayload, "invalid payload", nil)
			}
		}
		values := configAPIValues(cfg)
		if len(payload.Keys) > 0 {
			selected := make(map[string]any, len(payload.Keys))
			for _, key := range payload.Keys {
				if value, ok := values[key]; ok {
					selected[key] = value
				}
			}
			values = selected
		}
		data, _ := json.Marshal(values)
		return ipc.Response{OK: true, Data: data}
	})
	handler.Register(ipc.CmdConfigSchema, func(req ipc.Request) ipc.Response {
		data, _ := json.Marshal(configAPISchema())
		return ipc.Response{OK: true, Data: data}
	})
	handler.Register(ipc.CmdConfigSet, func(req ipc.Request) ipc.Response {
		var payload ipc.ConfigSetPayload
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return ipc.Err(ipc.CodeInvalidPayload, "invalid payload", nil)
		}
		values, err := configAPIWritableValues(payload.Values)
		if err != nil {
			return ipc.Err(ipc.CodePermissionDenied, err.Error(), nil)
		}
		path := filepath.Join(filepath.Dir(cfg.ConfigPath), "kcd.toml.d", "90-ipc.toml")
		if err := config.WriteAPIDropIn(path, values); err != nil {
			return ipc.Err(ipc.CodeInternal, "failed to write configuration", nil)
		}
		return ipc.Response{OK: true}
	})
	handler.Register(ipc.CmdReload, func(req ipc.Request) ipc.Response {
		data, _ := json.Marshal(ipc.ReloadResponse{Applied: []string{}, RestartRequired: []string{"config reload requires daemon SIGHUP"}})
		return ipc.Response{OK: true, Data: data}
	})
	handler.Register(ipc.CmdHello, func(req ipc.Request) ipc.Response {
		var payload ipc.HelloPayload
		if len(req.Payload) > 0 {
			if err := json.Unmarshal(req.Payload, &payload); err != nil {
				return ipc.Response{OK: false, Error: "invalid payload"}
			}
		}
		pluginState := make(map[string]bool)
		for _, name := range []string{"Pair", "Battery", "Clipboard", "Notification", "Share", "RunCommand", "MPRIS", "Ping", "Telephony", "Connectivity", "Mousepad", "SFTP", "FindMyPhone", "LockDevice", "SystemVolume", "SMS", "Contacts", "Presenter", "FindThisDevice", "RemoteSystemVolume"} {
			pluginState[name] = cfg.PluginEnabledForDevice("", name)
		}
		for _, p := range plugins.All() {
			pluginState[p.Name()] = true
		}
		data, err := json.Marshal(ipc.HelloResponse{
			APIVersion:    ipc.APIVersion,
			DaemonVersion: Version,
			Commands:      handler.Commands(),
			Events:        events.AllTypes(),
			OptInEvents:   eventNames(events.OptInOnly()),
			Plugins:       pluginState,
			Features:      []string{"errorCodes", "keepAlive", "watchResume", "prompts", "config", "schemas"},
		})
		if err != nil {
			return ipc.Response{OK: false, Error: "failed to build capability response"}
		}
		return ipc.Response{OK: true, Data: data}
	})

	if cfg.Plugins.Battery {
		registerBatteryRoutes(handler, devices, plugins)
	}
	if cfg.Plugins.Connectivity {
		registerConnectivityRoutes(handler, devices, plugins)
	}
	if cfg.Plugins.Clipboard {
		registerClipboardRoutes(handler, devices, plugins)
	}
	if cfg.Plugins.Contacts {
		registerContactsRoutes(handler, devices, plugins)
	}
	if cfg.Plugins.RunCommand {
		registerRunCommandRoutes(handler, devices, plugins)
	}
	if cfg.Plugins.Share {
		registerShareRoutes(handler, devices, plugins)
	}
	if cfg.Plugins.SFTP {
		registerSftpRoutes(handler, devices, plugins)
	}
	registerCommRoutes(handler, cfg, devices, plugins)
	registerDeviceRoutes(handler, cfg, devices, plugins)
	if cfg.Plugins.MPRIS {
		registerMprisRoutes(handler, devices, plugins)
	}
	if cfg.Plugins.RemoteSystemVolume {
		registerRemoteVolumeRoutes(handler, devices, plugins)
	}

	handler.Register(ipc.CmdConnect, func(req ipc.Request) ipc.Response {
		var p ipc.ConnectPayload
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return ipc.Response{OK: false, Error: "invalid payload"}
		}
		addr := net.ParseIP(p.IP)
		if addr == nil {
			return ipc.Response{OK: false, Error: "invalid IP address"}
		}

		go func() {
			// No target ID: the peer is whoever answers here. Empty omits
			// targetDeviceId; stock peers drop misaddressed identities.
			env.Dial(ctx, addr, env.cfg.TCPPort, "", protocol.ProtocolVersion, true)
		}()

		return ipc.Response{OK: true}
	})

	handler.Register(ipc.CmdBroadcastStart, func(req ipc.Request) ipc.Response {
		bc.Start(ctx)
		return ipc.Response{OK: true}
	})
	handler.Register(ipc.CmdBroadcastStop, func(req ipc.Request) ipc.Response {
		bc.Stop()
		// Pairing window closed: drop discovery connections that never paired
		// so strangers don't linger; pairs, in-flight requests, intents stay.
		for _, dev := range devices.List() {
			if !dev.IsConnected() || dev.State() != device.StateUnpaired || dev.PairDialActive() {
				continue
			}
			logger.Debug("broadcast stopped, dropping unpaired discovery connection",
				log.String("device_id", dev.ID()))
			dev.Disconnect()
		}
		return ipc.Response{OK: true}
	})

	handler.Register(ipc.CmdStatus, func(req ipc.Request) ipc.Response {
		uptime := time.Since(startedAt)
		h := int(uptime.Hours())
		m := int(uptime.Minutes()) % 60
		uptimeHuman := fmt.Sprintf("%dh %dm", h, m)

		pluginNames := make([]string, 0)
		for _, p := range plugins.All() {
			pluginNames = append(pluginNames, p.Name())
		}

		total := 0
		connected := 0
		devInfos := make([]ipc.StatusDevice, 0)
		for _, d := range devices.List() {
			total++
			isConnected := d.IsConnected()
			if isConnected {
				connected++
			}
			info := ipc.StatusDevice{
				ID:        d.ID(),
				Name:      d.Name(),
				Type:      d.Type,
				State:     d.State().String(),
				Connected: isConnected,
			}
			if ip := d.RemoteIP(); ip != nil && isConnected {
				info.Addr = ip.String()
				if port := d.LastPort(); port > 0 {
					info.Addr += fmt.Sprintf(":%d", port)
				}
			} else if ip := d.LastIP(); ip != nil {
				info.Addr = ip.String()
			}
			if charge, charging := d.GetBattery(); d.HasBattery() {
				info.Battery = &ipc.StatusBattery{
					Charge:   charge,
					Charging: charging,
					AgeMs:    d.BatteryAge().Milliseconds(),
				}
			}
			if lastSeen := d.LastSeen(); !lastSeen.IsZero() {
				info.LastSeen = lastSeen.UTC().Format(time.RFC3339)
			}
			devInfos = append(devInfos, info)
		}

		data, _ := json.Marshal(ipc.StatusResponse{
			Version:        Version,
			StartedAt:      startedAt.UTC().Format(time.RFC3339),
			UptimeHuman:    uptimeHuman,
			SocketPath:     cfg.SocketPath,
			ConfigPath:     cfg.ConfigPath,
			TCPPort:        cfg.TCPPort,
			Plugins:        pluginNames,
			DeviceCount:    total,
			ConnectedCount: connected,
			Devices:        devInfos,
		})
		return ipc.Response{OK: true, Data: data}
	})

	handler.Register(ipc.CmdMprisStatus, func(req ipc.Request) ipc.Response {
		pl, ok := plugins.GetByName("MPRIS")
		if !ok {
			return ipc.Response{OK: false, Error: "mpris plugin not enabled"}
		}
		status := pl.(*mpris.MPRISPlugin).DebugStatus()
		return jsonOK(status)
	})
}

func configAPIValues(cfg *config.Config) map[string]any {
	return map[string]any{
		"log_level":                  cfg.LogLevel,
		"device_name":                cfg.DeviceName,
		"device_type":                cfg.DeviceType,
		"share.accept":               cfg.Share.Accept,
		"share.text_action":          cfg.Share.TextAction,
		"share.url_action":           cfg.Share.URLAction,
		"clipboard.receive":          cfg.Clipboard.Receive,
		"clipboard.send":             cfg.Clipboard.Send,
		"popups.enabled":             cfg.Popups.Enabled,
		"popups.backend":             cfg.Popups.Backend,
		"runcommand.default_timeout": time.Duration(cfg.RunCommand.DefaultTimeout).String(),
	}
}

func configAPISchema() []map[string]any {
	return []map[string]any{
		{"key": "log_level", "type": "string", "live": true},
		{"key": "device_name", "type": "string", "live": false},
		{"key": "device_type", "type": "string", "live": false},
		{"key": "share.accept", "type": "string", "enum": []string{"always", "ask", "never"}, "live": false},
		{"key": "clipboard.receive", "type": "bool", "live": false},
		{"key": "clipboard.send", "type": "bool", "live": false},
		{"key": "popups.enabled", "type": "bool", "live": false},
		{"key": "popups.backend", "type": "string", "enum": []string{"auto", "notify-send", "dbus", "none"}, "live": false},
		{"key": "ipc.readonly_socket", "type": "string", "live": false},
	}
}

func configAPIWritableValues(raw map[string]json.RawMessage) (map[string]any, error) {
	allowed := map[string]string{
		"log_level":                  "string",
		"share.accept":               "string",
		"clipboard.receive":          "bool",
		"clipboard.send":             "bool",
		"popups.enabled":             "bool",
		"popups.backend":             "string",
		"runcommand.default_timeout": "string",
	}
	result := make(map[string]any)
	for key, data := range raw {
		expected, ok := allowed[key]
		if !ok {
			return nil, fmt.Errorf("configuration key %q is not writable through IPC", key)
		}
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			return nil, fmt.Errorf("invalid value for %s", key)
		}
		switch expected {
		case "string":
			if _, ok := value.(string); !ok {
				return nil, fmt.Errorf("configuration key %q requires a string", key)
			}
		case "bool":
			if _, ok := value.(bool); !ok {
				return nil, fmt.Errorf("configuration key %q requires a boolean", key)
			}
		}
		parts := strings.SplitN(key, ".", 2)
		if len(parts) == 1 {
			result[key] = value
			continue
		}
		section, ok := result[parts[0]].(map[string]any)
		if !ok {
			section = make(map[string]any)
			result[parts[0]] = section
		}
		section[parts[1]] = value
	}
	return result, nil
}

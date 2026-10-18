package sftp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

func (p *SftpPlugin) Handle(_ context.Context, dev device.Sender, pkt *protocol.Packet) error {
	var body SftpBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	if body.ErrorMessage != "" {
		p.mu.Lock()
		delete(p.lastBody, dev.ID())
		pending := p.pending[dev.ID()]
		p.mu.Unlock()
		if pending != nil {
			select {
			case pending <- sftpResult{err: fmt.Errorf("sftp: phone could not start server: %s", body.ErrorMessage)}:
			default:
			}
		}
		p.logger.Warn("sftp: server error from device",
			log.String("device_id", dev.ID()),
			log.String("error", body.ErrorMessage),
		)
		if p.bus != nil {
			p.bus.Publish(events.TypeSftpMount, dev.ID(), map[string]interface{}{
				"error": body.ErrorMessage,
			})
		}
		return nil
	}
	if !credentialsComplete(body) {
		return nil
	}

	p.mu.Lock()
	p.lastBody[dev.ID()] = body
	pending := p.pending[dev.ID()]
	p.mu.Unlock()
	if pending != nil {
		select {
		case pending <- sftpResult{body: body}:
		default:
		}
	}

	safeURI := fmt.Sprintf("sftp://%s@%s:%s%s", body.User, body.IP, body.Port.String(), body.Path)
	p.logger.Info("SFTP server available", log.String("uri", safeURI))

	evtPayload := map[string]interface{}{
		"uri":      fmt.Sprintf("sftp://%s:%s@%s:%s%s", body.User, body.Password, body.IP, body.Port.String(), body.Path),
		"ip":       body.IP,
		"port":     body.Port.String(),
		"user":     body.User,
		"password": body.Password,
		"path":     body.Path,
	}
	if len(body.MultiPaths) > 0 {
		volumes := make([]map[string]string, 0, len(body.MultiPaths))
		for i, mp := range body.MultiPaths {
			name := mp
			if i < len(body.PathNames) {
				name = body.PathNames[i]
			}
			volumes = append(volumes, map[string]string{"name": name, "path": mp})
		}
		evtPayload["volumes"] = volumes
	}

	if p.bus != nil {
		p.bus.Publish(events.TypeSftpMount, dev.ID(), evtPayload)
	}

	return nil
}

func credentialsComplete(body SftpBody) bool {
	port, err := strconv.Atoi(body.Port.String())
	return err == nil && body.IP != "" && port > 0 && port <= 65535 && body.User != "" && body.Password != ""
}

func (p *SftpPlugin) OnConnect(_ device.Sender) {}

func (p *SftpPlugin) OnDisconnect(dev device.Sender) {
	deviceID := dev.ID()
	// Via MountedPath, so a mount adopted from the kernel is cleaned up too
	// rather than surviving every disconnect untouched.
	if p.MountedPath(deviceID) != "" {
		p.logger.Info("device disconnected, cleaning up SFTP mount",
			log.String("device_id", deviceID),
		)
		if err := p.Unmount(deviceID); err != nil {
			p.logger.Warn("failed to unmount on disconnect",
				log.String("device_id", deviceID),
				log.Error(err),
			)
		}
	}

	// Evict cached credentials to prevent slow memory leak.
	p.mu.Lock()
	delete(p.lastBody, deviceID)
	p.mu.Unlock()
}

// mountIsReadOnly reports whether the device's existing mount is read-only.
// False for a mount kcd did not create or no longer tracks.

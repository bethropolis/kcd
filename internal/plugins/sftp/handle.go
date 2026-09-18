package sftp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

func (p *SftpPlugin) Handle(_ context.Context, dev device.Sender, pkt *protocol.Packet) error {
	var body SftpBody
	if err := json.Unmarshal(pkt.Body, &body); err != nil {
		return err
	}

	if body.ErrorMessage != "" {
		p.logger.Warn("SFTP server error from device",
			zap.String("device_id", dev.ID()),
			zap.String("error", body.ErrorMessage),
		)
		if p.bus != nil {
			p.bus.Publish(events.TypeSftpMount, dev.ID(), map[string]interface{}{
				"error": body.ErrorMessage,
			})
		}
		return nil
	}

	p.mu.Lock()
	p.lastBody[dev.ID()] = body
	p.mu.Unlock()

	safeURI := fmt.Sprintf("sftp://%s@%s:%s%s", body.User, body.IP, body.Port.String(), body.Path)
	p.logger.Info("SFTP server available", zap.String("uri", safeURI))

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

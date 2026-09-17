package sftp

import (
	"context"
	"fmt"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

// RequestMount sends a kdeconnect.sftp.request packet asking the device to
// start its SFTP server and return credentials.
func (p *SftpPlugin) RequestMount(dev device.Sender) error {
	pkt, err := protocol.NewPacket("kdeconnect.sftp.request", map[string]any{
		"startBrowsing": true,
	})
	if err != nil {
		return err
	}
	return dev.Send(pkt)
}

// RequestAndMount sends the SFTP request, waits for the Android device to
// respond with credentials (up to 20 s), mounts the filesystem via sshfs,
// and returns the local path the user should open.
func (p *SftpPlugin) RequestAndMount(ctx context.Context, dev device.Sender) (string, error) {
	if p.bus == nil {
		return "", fmt.Errorf("event bus not available")
	}

	// Subscribe BEFORE sending the request to guarantee we don't miss the response.
	sub := p.bus.Subscribe(0, events.TypeSftpMount)
	defer sub.Close()

	if err := p.RequestMount(dev); err != nil {
		return "", fmt.Errorf("send SFTP request: %w", err)
	}

	p.logger.Info("SFTP request sent, waiting for phone response", zap.String("device", dev.ID()))

	timeout := time.Duration(p.cfg.CredentialsTimeoutSecs) * time.Second
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		select {
		case evt, ok := <-sub.C:
			if !ok {
				return "", fmt.Errorf("event bus closed")
			}
			if evt.DeviceID != dev.ID() {
				continue
			}
			p.mu.RLock()
			body, exists := p.lastBody[dev.ID()]
			p.mu.RUnlock()
			if !exists {
				return "", fmt.Errorf("credentials missing after event (internal error)")
			}
			return p.mountWithBody(ctx, dev.ID(), body, "")

		case <-deadline.Done():
			return "", fmt.Errorf("timed out after %s waiting for SFTP response — is the KDE Connect app open on the phone?", timeout)
		}
	}
}

// RequestAndMountVolume sends the SFTP request, waits for credentials, then
// mounts the specified volume. If volumePath is empty, the available volumes
// are returned without mounting (list mode). The caller is responsible for
// closing the returned closer when done with the mounted path.
func (p *SftpPlugin) RequestAndMountVolume(ctx context.Context, dev device.Sender, volumePath string) (mountPath string, volumes []StorageVolume, err error) {
	if p.bus == nil {
		return "", nil, fmt.Errorf("event bus not available")
	}

	sub := p.bus.Subscribe(0, events.TypeSftpMount)
	defer sub.Close()

	if err := p.RequestMount(dev); err != nil {
		return "", nil, fmt.Errorf("send SFTP request: %w", err)
	}

	p.logger.Info("SFTP request sent, waiting for phone response", zap.String("device", dev.ID()))

	timeout := time.Duration(p.cfg.CredentialsTimeoutSecs) * time.Second
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		select {
		case evt, ok := <-sub.C:
			if !ok {
				return "", nil, fmt.Errorf("event bus closed")
			}
			if evt.DeviceID != dev.ID() {
				continue
			}
			p.mu.RLock()
			body, exists := p.lastBody[dev.ID()]
			p.mu.RUnlock()
			if !exists {
				return "", nil, fmt.Errorf("credentials missing after event (internal error)")
			}

			vols := p.buildVolumes(body)

			if volumePath == "" {
				return "", vols, nil
			}

			path, err := p.mountWithBody(ctx, dev.ID(), body, volumePath)
			if err != nil {
				return "", nil, err
			}
			return path, vols, nil

		case <-deadline.Done():
			return "", nil, fmt.Errorf("timed out after %s waiting for SFTP response — is the KDE Connect app open on the phone?", timeout)
		}
	}
}

// MountLocally mounts using previously cached credentials.
// Prefer RequestAndMount for a one-step experience.
func (p *SftpPlugin) MountLocally(ctx context.Context, deviceID string) (string, error) {
	p.mu.RLock()
	body, ok := p.lastBody[deviceID]
	p.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("no SFTP credentials cached for device %s — use 'kcd sftp mount' which requests them automatically", deviceID)
	}
	return p.mountWithBody(ctx, deviceID, body, "")
}

// Info returns the cached SFTP connection details for a device.
// Returns nil if no credentials have been received yet.
func (p *SftpPlugin) Info(deviceID string) *SftpInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	body, ok := p.lastBody[deviceID]
	if !ok {
		return nil
	}
	info := &SftpInfo{
		IP:       body.IP,
		Port:     body.Port,
		User:     body.User,
		Password: body.Password,
		Path:     body.Path,
	}
	for i, mp := range body.MultiPaths {
		name := mp
		if i < len(body.PathNames) {
			name = body.PathNames[i]
		}
		info.Volumes = append(info.Volumes, StorageVolume{Name: name, Path: mp})
	}
	return info
}

// buildVolumes constructs a StorageVolume slice from a SftpBody.
// Caller must hold at least a read lock on p.mu if body comes from p.lastBody.
func (p *SftpPlugin) buildVolumes(body SftpBody) []StorageVolume {
	if len(body.MultiPaths) == 0 {
		return nil
	}
	volumes := make([]StorageVolume, 0, len(body.MultiPaths))
	for i, mp := range body.MultiPaths {
		name := mp
		if i < len(body.PathNames) {
			name = body.PathNames[i]
		}
		volumes = append(volumes, StorageVolume{Name: name, Path: mp})
	}
	return volumes
}

// Volumes returns the list of available storage volumes from cached credentials.
// Returns nil if no credentials or no multiPaths data.
func (p *SftpPlugin) Volumes(deviceID string) []StorageVolume {
	p.mu.RLock()
	defer p.mu.RUnlock()
	body, ok := p.lastBody[deviceID]
	if !ok {
		return nil
	}
	return p.buildVolumes(body)
}

// MountedPath returns the local mount point for a device, or "" if not mounted.
func (p *SftpPlugin) MountedPath(deviceID string) string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mountPoints[deviceID]
}

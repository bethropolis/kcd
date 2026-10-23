package sftp

import (
	"context"
	"fmt"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
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

// RequestAndMount requests credentials, mounts via sshfs, and returns the
// local path to open.
func (p *SftpPlugin) RequestAndMount(ctx context.Context, dev device.Sender, readOnly bool) (string, error) {
	body, err := p.requestCredentials(ctx, dev)
	if err != nil {
		return "", err
	}
	path, err := p.mountWithBody(ctx, dev.ID(), body, "", readOnly)
	p.clearPassword(dev.ID(), body.Password)
	return path, err
}

// RequestAndMountVolume requests credentials and mounts the volume (empty
// volumePath lists instead). The caller closes the returned closer when done.
func (p *SftpPlugin) RequestAndMountVolume(ctx context.Context, dev device.Sender, volumePath string, readOnly bool) (mountPath string, volumes []StorageVolume, err error) {
	body, err := p.requestCredentials(ctx, dev)
	if err != nil {
		return "", nil, err
	}
	volumes = p.buildVolumes(body)
	if volumePath == "" {
		return "", volumes, nil
	}
	path, err := p.mountWithBody(ctx, dev.ID(), body, volumePath, readOnly)
	p.clearPassword(dev.ID(), body.Password)
	if err != nil {
		return "", nil, err
	}
	return path, volumes, nil
}

// MountLocally mounts using previously cached credentials.
// Prefer RequestAndMount for a one-step experience.
func (p *SftpPlugin) MountLocally(ctx context.Context, deviceID string, readOnly bool) (string, error) {
	return "", fmt.Errorf("sftp: cached credentials are not reused for mounts; request fresh credentials instead")
}

func (p *SftpPlugin) requestCredentials(ctx context.Context, dev device.Sender) (SftpBody, error) {
	id := dev.ID()
	ch := make(chan sftpResult, 1)
	p.mu.Lock()
	if _, exists := p.pending[id]; exists {
		p.mu.Unlock()
		return SftpBody{}, fmt.Errorf("sftp: a credential request is already pending for device %s", id)
	}
	delete(p.lastBody, id)
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		if p.pending[id] == ch {
			delete(p.pending, id)
		}
		p.mu.Unlock()
	}()
	if err := p.RequestMount(dev); err != nil {
		return SftpBody{}, fmt.Errorf("sftp: send request: %w", err)
	}
	p.logger.Info("sftp: request sent, waiting for phone response", log.String("device_id", id))
	timeout := p.credentialsTimeout()
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case result := <-ch:
		if result.err != nil {
			return SftpBody{}, result.err
		}
		return result.body, nil
	case <-deadline.Done():
		return SftpBody{}, fmt.Errorf("sftp: timed out after %s waiting for phone credentials", timeout)
	}
}

func (p *SftpPlugin) clearPassword(deviceID, password string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	body, ok := p.lastBody[deviceID]
	if ok && body.Password == password {
		body.Password = ""
		p.lastBody[deviceID] = body
	}
}

// Info returns cached SFTP details, password only on request: it is a
// working credential that would otherwise land in scrollback and logs.
// Nil means uncached credentials, even while still mounted.
func (p *SftpPlugin) Info(deviceID string, includePassword bool) *SftpInfo {
	p.mu.RLock()
	body, ok := p.lastBody[deviceID]
	var volumes []StorageVolume
	if ok {
		volumes = p.buildVolumes(body)
	}
	p.mu.RUnlock()

	if !ok {
		return nil
	}
	mountPoint := p.MountedPath(deviceID)
	info := &SftpInfo{
		IP:         body.IP,
		Port:       body.Port,
		User:       body.User,
		Path:       body.Path,
		Volumes:    volumes,
		Mounted:    mountPoint != "",
		MountPoint: mountPoint,
	}
	if includePassword {
		info.Password = body.Password
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

// MountedPath returns the local mount point, or "" if not mounted. Misses
// reconcile against the kernel table, so pre-restart mounts still report.
func (p *SftpPlugin) MountedPath(deviceID string) string {
	p.mu.RLock()
	mountPoint, cached := p.mountPoints[deviceID]
	p.mu.RUnlock()
	if cached {
		return mountPoint
	}

	// The kernel is the source of truth: configured location first, then the
	// previous default, so pre-move mounts are found, not orphaned.
	primary := p.mountPointFor(deviceID)
	if primary != "" {
		mountPoint = p.adoptIfMounted(deviceID, primary)
	}
	if mountPoint != "" {
		return mountPoint
	}
	if legacy := legacyMountPointFor(deviceID); legacy != "" && legacy != primary {
		if mountPoint = p.adoptIfMounted(deviceID, legacy); mountPoint != "" {
			p.logger.Info("adopted SFTP mount left at the previous default location",
				log.String("device_id", deviceID),
				log.String("mount_point", mountPoint),
			)
			return mountPoint
		}
	}
	return ""
}

// adoptIfMounted caches mountPoint as this device's mount when the kernel
// reports it, and returns it; otherwise returns "".
func (p *SftpPlugin) adoptIfMounted(deviceID, mountPoint string) string {
	if mountPoint == "" {
		return ""
	}
	if !mountExists(mountPoint) {
		return ""
	}
	p.mu.Lock()
	p.mountPoints[deviceID] = mountPoint
	p.mu.Unlock()
	p.logger.Debug("adopted SFTP mount left by a previous daemon session",
		log.String("device_id", deviceID),
		log.String("mount_point", mountPoint),
	)
	return mountPoint
}

// IsMounted reports whether a device's filesystem is currently mounted.
func (p *SftpPlugin) IsMounted(deviceID string) bool {
	return p.MountedPath(deviceID) != ""
}

// credentialsTimeout bounds the wait for the phone's SFTP response; the CLI
// derives its own deadline from it so the daemon reports the timeout.
func (p *SftpPlugin) credentialsTimeout() time.Duration {
	if p.cfg.CredentialsTimeoutSecs <= 0 {
		return 20 * time.Second
	}
	return time.Duration(p.cfg.CredentialsTimeoutSecs) * time.Second
}

// ReadOnlyByDefault reports the configured read-only default, used when a
// mount request does not override it.
func (p *SftpPlugin) ReadOnlyByDefault() bool {
	return p.cfg.ReadOnly
}

package device

import (
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

// Save writes one consistent registry listing to disk.
func (r *Registry) Save(path string) error {
	r.saveMu.Lock()
	defer r.saveMu.Unlock()
	devices := r.List()
	infos := make([]DeviceInfo, 0, len(devices))
	for _, dev := range devices {
		infos = append(infos, dev.Info())
	}
	return SaveDevices(path, infos)
}

// Load restores persisted devices and discards pairing requests that cannot survive a restart.
func (r *Registry) Load(path string, logger log.Logger) error {
	infos, err := LoadDevices(path)
	if err != nil {
		return err
	}
	for _, info := range infos {
		switch info.State {
		case StateUnknown, StatePairRequested, StatePairRequestedByPeer:
			info.State = StateUnpaired
		}
		info.Name = protocol.SanitizeDeviceName(info.Name)
		dev := NewDevice(info.ID, info.Name, info.Type, logger)
		dev.SetState(info.State)
		dev.CertFP = info.CertFP
		dev.SetLastSeen(info.LastSeen)
		if ip, port := info.DialTarget(); ip != nil {
			dev.SetLastIP(ip)
			dev.SetLastPort(port)
		}
		r.Add(dev)
	}
	return nil
}

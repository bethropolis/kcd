package share

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/protocol"
	"go.uber.org/zap"
)

func (p *SharePlugin) SendFile(ctx context.Context, dev device.Sender, filePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("share: open file: %w", err)
	}
	stat, err := f.Stat()
	f.Close() // Close immediately; AcceptAndSend opens it again exactly when the phone connects.
	if err != nil {
		return fmt.Errorf("share: stat file: %w", err)
	}

	if stat.IsDir() {
		return fmt.Errorf("share: directory transfer is not supported")
	}

	// Bind to an available side-channel port (using config range)
	ln, port, err := ListenSideChannel(ctx, p.cfg, p.TLSConfig)
	if err != nil {
		return err
	}

	var onProgress func(int64, int64)
	if p.bus != nil {
		throttle := newProgressThrottle(p.bus, dev.ID(), filepath.Base(filePath), stat.Size())
		onProgress = throttle.Update
	}
	expectedFP := cert.PinnedFingerprint(dev.PeerCert())

	// Handle the transfer in the background so IPC returns instantly
	go func() {
		defer debug.FreeOSMemory()

		timeout := time.Duration(p.cfg.AcceptTimeoutSecs) * time.Second
		if timeout == 0 {
			timeout = 2 * time.Minute
		}
		err := AcceptAndSend(ln, filePath, p.TLSConfig, dev.ID(), expectedFP, timeout, onProgress, p.Logger, p.sidechannel)

		if err != nil {
			p.Logger.Error("share: send failed",
				zap.String("device_id", dev.ID()),
				zap.String("file", filepath.Base(filePath)),
				zap.Int("port", port),
				zap.Error(err),
			)
		} else {
			p.Logger.Info("share: send complete",
				zap.String("device_id", dev.ID()),
				zap.String("file", filepath.Base(filePath)),
			)
		}

		if p.bus != nil {
			payload := map[string]interface{}{
				"file":    filepath.Base(filePath),
				"success": err == nil,
			}
			if err != nil {
				payload["error"] = err.Error()
			}
			p.bus.Publish(events.TypeShareComplete, dev.ID(), payload)
		}
	}()

	modTime := stat.ModTime().UnixMilli()

	// Send invite packet with strict metadata
	pkt, err := protocol.NewPacket("kdeconnect.share.request", ShareBody{
		Filename:         filepath.Base(filePath),
		NumberOfFiles:    1,
		TotalPayloadSize: stat.Size(),
		LastModified:     modTime,
		CreationTime:     modTime,
	})
	if err != nil {
		ln.Close()
		return err
	}

	pkt.PayloadSize = stat.Size()
	pkt.PayloadTransferInfo = &protocol.TransferInfo{
		Port: port,
	}

	p.Logger.Info("share: sending transfer invitation",
		zap.String("device_id", dev.ID()),
		zap.String("path", filePath),
		zap.Int64("size", pkt.PayloadSize),
		zap.Int("port", port),
	)

	return dev.Send(pkt)
}

func (p *SharePlugin) OnConnect(dev device.Sender)    {}
func (p *SharePlugin) OnDisconnect(dev device.Sender) {}

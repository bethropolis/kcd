package share

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"

	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/transport"
)

func ReceiveSideChannel(ctx context.Context, ip net.IP, port int, size int64, dest string, tlsConfig *tls.Config, expectedFP string, onProgress func(int64, int64), logger log.Logger, options ...transport.SidechannelOptions) error {
	var opts transport.SidechannelOptions
	if len(options) > 0 {
		opts = options[0]
	}
	maxSize := opts.MaxPayloadSize
	if maxSize <= 0 {
		maxSize = 1024 * 1024 * 1024
	}
	_, err := transport.ReceiveToFile(ctx, ip, port, size, dest, tlsConfig, expectedFP, logger, transport.ReceiveOptions{MaxSize: maxSize, OnProgress: onProgress, Sidechannel: opts})
	if err != nil {
		return fmt.Errorf("share: receive payload: %w", err)
	}
	return nil
}

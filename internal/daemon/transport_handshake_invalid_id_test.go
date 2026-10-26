package daemon

import (
	"context"
	"crypto/tls"
	"net"
	"strings"
	"testing"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/plugin"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/transport"
)

func TestHandleNewConnectionRejectsInvalidPeerID(t *testing.T) {
	localCert, err := cert.GenerateSelfSigned("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	peerCert, err := cert.GenerateSelfSigned("../invalid")
	if err != nil {
		t.Fatal(err)
	}
	serverRaw, clientRaw := net.Pipe()
	t.Cleanup(func() {
		_ = serverRaw.Close()
		_ = clientRaw.Close()
	})
	serverTLS := tls.Server(serverRaw, &tls.Config{
		Certificates: []tls.Certificate{*localCert},
		ClientAuth:   tls.RequireAnyClientCert,
	})
	clientTLS := tls.Client(clientRaw, &tls.Config{
		Certificates:       []tls.Certificate{*peerCert},
		InsecureSkipVerify: true,
	})
	localIdentity, err := protocol.NewIdentityPacket("0123456789abcdef0123456789abcdef", "Local", "desktop", 1716, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer protocol.ReleasePacket(localIdentity)
	peerIdentity, err := protocol.NewIdentityPacket("../invalid", "Peer", "phone", 1716, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer protocol.ReleasePacket(peerIdentity)

	devices := device.NewRegistry(nil)
	result := make(chan error, 1)
	go func() {
		result <- handleNewConnection(context.Background(), transport.NewConn(serverTLS), localIdentity, &protocol.IdentityBody{DeviceID: "peer_id_123456789012345678901234567890", ProtocolVersion: protocol.ProtocolVersion}, "", devices, plugin.NewRegistry(log.Nop()), "0123456789abcdef0123456789abcdef", nil, log.Nop(), config.Defaults())
	}()
	clientConn := transport.NewConn(clientTLS)
	localPkt, err := clientConn.ReadPacket()
	if err != nil {
		t.Fatal(err)
	}
	protocol.ReleasePacket(localPkt)
	if err := clientConn.WritePacket(peerIdentity); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err == nil || !strings.Contains(err.Error(), "invalid device ID") {
		t.Fatalf("handleNewConnection error = %v, want invalid device ID", err)
	}
	if _, ok := devices.Get("../invalid"); ok {
		t.Fatal("invalid peer was added to the registry")
	}
}

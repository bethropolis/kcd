package transport

import (
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/cert"
	"github.com/bethropolis/kcd/internal/protocol"
)

func TestConn_WriteReadPacket(t *testing.T) {
	// Create an in-memory TLS pair
	cCert, err := cert.GenerateSelfSigned("client_test_device")
	if err != nil {
		t.Fatal(err)
	}
	sCert, err := cert.GenerateSelfSigned("server_test_device")
	if err != nil {
		t.Fatal(err)
	}

	serverCfg := cert.TLSConfig(sCert)
	clientCfg := cert.TLSConfig(cCert)
	clientCfg.ServerName = "kcd" // for SNI if necessary, but skipVerify handles validation

	clientNet, serverNet := net.Pipe()

	// Create channels for async server
	errCh := make(chan error, 1)
	pktCh := make(chan *protocol.Packet, 1)

	// Run TLS server
	go func() {
		tlsServer := tls.Server(serverNet, serverCfg)
		if err := tlsServer.Handshake(); err != nil {
			errCh <- err
			return
		}
		sConn := NewConn(tlsServer)
		defer sConn.Close()

		pkt, err := sConn.ReadPacket()
		if err != nil {
			errCh <- err
		} else {
			pktCh <- pkt
			errCh <- nil
		}
	}()

	// Run TLS client
	tlsClient := tls.Client(clientNet, clientCfg)
	if err := tlsClient.Handshake(); err != nil {
		t.Fatalf("client handshake failed: %v", err)
	}

	cConn := NewConn(tlsClient)
	defer cConn.Close()

	// Send a packet
	sendPkt, _ := protocol.NewIdentityPacket("id1", "name1", "desktop", 1716, nil, nil)
	if err := cConn.WritePacket(sendPkt); err != nil {
		t.Fatalf("WritePacket failed: %v", err)
	}

	// Wait for server to receive
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("server error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for server")
	}

	recvPkt := <-pktCh
	if recvPkt.Type != protocol.TypeIdentity {
		t.Errorf("expected identity type, got %s", recvPkt.Type)
	}
}

package sftp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
	"github.com/bethropolis/kcd/internal/testutil"
)

func TestServerNotRunningDoesNotReplaceCredentials(t *testing.T) {
	bus := events.NewBus(log.Nop())
	sub := bus.Subscribe(4, events.TypeSftpMount)
	defer sub.Close()
	p := NewSftpPlugin(config.SFTPConfig{}, bus, log.Nop())
	dev := testutil.NewFakeSender("phone")
	valid := SftpBody{IP: "192.0.2.2", Port: "1739", User: "u0_a1", Password: "secret", Path: "/storage"}
	pkt, _ := protocol.NewPacket("kdeconnect.sftp", valid)
	if err := p.Handle(context.Background(), dev, pkt); err != nil {
		t.Fatal(err)
	}
	<-sub.C
	status, _ := protocol.NewPacket("kdeconnect.sftp", json.RawMessage(`{"serverRunning":false}`))
	if err := p.Handle(context.Background(), dev, status); err != nil {
		t.Fatal(err)
	}
	if got := p.lastBody[dev.ID()]; got.Password != valid.Password {
		t.Fatalf("status packet replaced credentials: %+v", got)
	}
	select {
	case ev := <-sub.C:
		t.Fatalf("status packet emitted credential event: %+v", ev)
	default:
	}
}

func TestRequestReturnsPhoneSFTPErrorInsteadOfStaleCredentials(t *testing.T) {
	cfg := config.SFTPConfig{CredentialsTimeoutSecs: 1}
	bus := events.NewBus(log.Nop())
	p := NewSftpPlugin(cfg, bus, log.Nop())
	p.lastBody["phone"] = SftpBody{IP: "192.0.2.3", Port: "1740", User: "old", Password: "stale", Path: "/storage", MultiPaths: []string{"/old"}}
	dev := testutil.NewFakeSender("phone")
	type result struct {
		volumes []StorageVolume
		err     error
	}
	done := make(chan result, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _, volumes, err := p.RequestAndMountVolume(ctx, dev, "", false); done <- result{volumes, err} }()
	if dev.WaitFor("kdeconnect.sftp.request", time.Second) == nil {
		t.Fatal("request packet was not sent")
	}
	errorPkt, _ := protocol.NewPacket("kdeconnect.sftp", json.RawMessage(`{"errorMessage":"Storage permission denied"}`))
	if err := p.Handle(context.Background(), dev, errorPkt); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err == nil || !strings.Contains(got.err.Error(), "Storage permission denied") {
			t.Fatalf("request result = %v, want phone error", got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not receive phone error")
	}
}

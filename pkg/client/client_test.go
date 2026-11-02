package client

import (
	"bufio"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/bethropolis/kcd/internal/ipc"
)

func TestNew(t *testing.T) {
	client := New("/tmp/kcd.sock")
	if client.SocketPath != "/tmp/kcd.sock" {
		t.Fatalf("SocketPath = %q", client.SocketPath)
	}
}

func TestHelloUsesCurrentAPIVersion(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "kcd.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()

		var request ipc.Request
		if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&request); err != nil {
			serverErr <- err
			return
		}
		var payload ipc.HelloPayload
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			serverErr <- err
			return
		}
		if request.Command != ipc.CmdHello || payload.Client != "pkg/client" || len(payload.APIVersions) != 1 || payload.APIVersions[0] != APIVersion {
			serverErr <- errUnexpectedHelloRequest
			return
		}
		response := ipc.Response{OK: true}
		response.Data, _ = json.Marshal(ipc.HelloResponse{APIVersion: APIVersion, DaemonVersion: "test"})
		serverErr <- json.NewEncoder(conn).Encode(response)
	}()

	client := New(socketPath)
	client.Timeout = time.Second
	hello, err := client.Hello()
	if err != nil {
		t.Fatal(err)
	}
	if hello.APIVersion != APIVersion {
		t.Fatalf("APIVersion = %d, want %d", hello.APIVersion, APIVersion)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}

var errUnexpectedHelloRequest = errorString("unexpected hello request")

type errorString string

func (e errorString) Error() string { return string(e) }

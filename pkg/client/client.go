// Package client implements the local IPC client for kcdctl.
package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/bethropolis/kcd/internal/ipc"
)

// Client connects to the kcd daemon via Unix socket.
type Client struct {
	SocketPath string
	Timeout    time.Duration
	// PairListenTimeout is the client's deadline for a pair_listen call.
	// Set it above the daemon's pairing.listen_timeout; zero defaults to 70s.
	PairListenTimeout time.Duration
}

// Call dialed the daemon, sends a request, and returns the response.
func (c *Client) Call(cmd string, payload interface{}) (*ipc.Response, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", c.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("kcd daemon not running or socket error: %w", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(timeout))

	var rawPayload []byte
	if payload != nil {
		rawPayload, err = json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal payload: %w", err)
		}
	}

	req := ipc.Request{
		Command: cmd,
		Payload: rawPayload,
	}

	reqBytes, _ := json.Marshal(req)
	reqBytes = append(reqBytes, '\n')

	if _, err := conn.Write(reqBytes); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	resBytes, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var res ipc.Response
	if err := json.Unmarshal(resBytes, &res); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if !res.OK {
		return nil, fmt.Errorf("daemon error: %s", res.Error)
	}

	return &res, nil
}

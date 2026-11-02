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

// APIVersion is the version of the additive IPC contract supported by Client.
const APIVersion = ipc.APIVersion

// Client connects to the kcd daemon via Unix socket.
type Client struct {
	SocketPath string
	Timeout    time.Duration
	// PairListenTimeout is the client's deadline for a pair_listen call.
	// Set it above the daemon's pairing.listen_timeout; zero defaults to 70s.
	PairListenTimeout time.Duration
	// SftpTimeout covers the calls waiting on the phone and must exceed the
	// daemon's own deadline, or failures misreport as connection timeouts.
	// Zero defaults to 60s.
	SftpTimeout time.Duration
}

// New creates a client for the daemon listening at socketPath.
func New(socketPath string) *Client {
	return &Client{SocketPath: socketPath}
}

// Hello discovers the daemon's supported IPC commands, events, plugins, and
// additive features.
func (c *Client) Hello() (*ipc.HelloResponse, error) {
	res, err := c.Call(ipc.CmdHello, ipc.HelloPayload{
		Client:      "pkg/client",
		APIVersions: []int{APIVersion},
	})
	if err != nil {
		return nil, err
	}
	var hello ipc.HelloResponse
	if err := json.Unmarshal(res.Data, &hello); err != nil {
		return nil, fmt.Errorf("decode hello response: %w", err)
	}
	return &hello, nil
}

// ConfigSet writes allowlisted configuration values to an IPC drop-in.
func (c *Client) ConfigSet(values map[string]any) error {
	_, err := c.Call(ipc.CmdConfigSet, ipc.ConfigSetPayload{Values: marshalConfigValues(values)})
	return err
}

func marshalConfigValues(values map[string]any) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(values))
	for key, value := range values {
		if data, err := json.Marshal(value); err == nil {
			result[key] = data
		}
	}
	return result
}

// Call dialed the daemon, sends a request, and returns the response.
func (c *Client) Call(cmd string, payload interface{}) (*ipc.Response, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return c.callWithTimeout(cmd, payload, timeout)
}

// callWithTimeout is Call with an explicit deadline, for the few commands whose
// daemon-side wait is longer than the default client deadline.
func (c *Client) callWithTimeout(cmd string, payload interface{}, timeout time.Duration) (*ipc.Response, error) {
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
		if res.Code != "" {
			return nil, &Error{Code: res.Code, Message: res.Error, Details: res.Details}
		}
		return nil, fmt.Errorf("daemon error: %s", res.Error)
	}

	return &res, nil
}

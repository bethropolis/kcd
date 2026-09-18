package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/ipc"
)

// Watch subscribes to daemon events and streams them to the given channel.
func (c *Client) Watch(ctx context.Context, filter []string, ch chan<- events.Event) error {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", c.SocketPath)
	if err != nil {
		return fmt.Errorf("kcd daemon not running or socket error: %w", err)
	}

	payload, _ := json.Marshal(ipc.WatchPayload{Events: filter})
	req := ipc.Request{
		Command: ipc.CmdWatch,
		Payload: payload,
	}

	reqBytes, _ := json.Marshal(req)
	reqBytes = append(reqBytes, '\n')

	if _, err := conn.Write(reqBytes); err != nil {
		conn.Close()
		return fmt.Errorf("write request: %w", err)
	}

	reader := bufio.NewReader(conn)
	resBytes, err := reader.ReadBytes('\n')
	if err != nil {
		conn.Close()
		return fmt.Errorf("read response: %w", err)
	}

	var res ipc.Response
	if err := json.Unmarshal(resBytes, &res); err != nil {
		conn.Close()
		return fmt.Errorf("unmarshal response: %w", err)
	}

	if !res.OK {
		conn.Close()
		return fmt.Errorf("daemon error: %s", res.Error)
	}

	defer conn.Close()

	// Create a goroutine to close the connection if context is canceled
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("stream read error: %w", err)
		}

		var ev events.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		select {
		case ch <- ev:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

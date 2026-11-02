package client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/ipc"
)

// Session is an opt-in persistent IPC connection negotiated with hello.
type Session struct {
	conn   net.Conn
	reader *bufio.Reader
	mu     sync.Mutex
	next   uint64
}

// OpenKeepAlive opens a persistent IPC session.
func (c *Client) OpenKeepAlive() (*Session, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	conn, err := net.DialTimeout("unix", c.SocketPath, timeout)
	if err != nil {
		return nil, fmt.Errorf("kcd daemon not running or socket error: %w", err)
	}
	session := &Session{conn: conn, reader: bufio.NewReader(conn), next: 2}
	if err := session.writeRequest(1, ipc.CmdHello, ipc.HelloPayload{KeepAlive: true}, timeout); err != nil {
		conn.Close()
		return nil, err
	}
	if _, err := session.readResponse(timeout); err != nil {
		conn.Close()
		return nil, err
	}
	return session, nil
}

// Call sends one request over the persistent session.
func (s *Session) Call(command string, payload any, timeout time.Duration) (*ipc.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	id := s.next
	s.next++
	if err := s.writeRequest(id, command, payload, timeout); err != nil {
		return nil, err
	}
	return s.readResponse(timeout)
}

// Close closes the persistent IPC connection.
func (s *Session) Close() error { return s.conn.Close() }

func (s *Session) writeRequest(id uint64, command string, payload any, timeout time.Duration) error {
	var raw json.RawMessage
	var err error
	if payload != nil {
		raw, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
	}
	req := ipc.Request{Command: command, ID: json.RawMessage(fmt.Sprintf("%d", id)), Payload: raw}
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')
	if err := s.conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	if _, err := s.conn.Write(data); err != nil {
		return fmt.Errorf("write request: %w", err)
	}
	return nil
}

func (s *Session) readResponse(timeout time.Duration) (*ipc.Response, error) {
	if err := s.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	data, err := s.reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	var response ipc.Response
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if !response.OK {
		if response.Code != "" {
			return nil, &Error{Code: response.Code, Message: response.Error, Details: response.Details}
		}
		return nil, fmt.Errorf("daemon error: %s", response.Error)
	}
	return &response, nil
}

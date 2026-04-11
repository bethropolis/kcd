package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/bethropolis/kcd/internal/events"
	"go.uber.org/zap"
)

// Server handles listening for JSON IPC requests over a Unix socket.
type Server struct {
	path    string
	handler *Handler
	logger  *zap.Logger
}

// NewServer creates a new IPC server.
func NewServer(path string, handler *Handler, logger *zap.Logger) *Server {
	return &Server{
		path:    path,
		handler: handler,
		logger:  logger.With(zap.String("component", "ipc")),
	}
}

// Listen starts listening on the Unix socket and processes incoming connections.
func (s *Server) Listen(ctx context.Context) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	// Always attempt to remove the old socket if it exists
	_ = os.Remove(s.path)

	l, err := net.Listen("unix", s.path)
	if err != nil {
		return err
	}

	// Restrict socket permissions to the current user (Fault Tolerance & Security Phase 2)
	if err := os.Chmod(s.path, 0600); err != nil {
		l.Close()
		return fmt.Errorf("failed to chmod ipc socket: %w", err)
	}

	go func() {
		<-ctx.Done()
		l.Close()
		os.Remove(s.path)
	}()

	s.logger.Info("ipc server started", zap.String("path", s.path))

	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			s.logger.Error("ipc socket accept error", zap.Error(err))
			continue
		}

		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	// Read a single line (one request per connection)
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		conn.Close()
		return
	}

	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		s.writeResponse(conn, Response{OK: false, Error: "malformed request"})
		conn.Close()
		return
	}

	if req.Command == CmdWatch {
		s.handleWatch(conn, req.Payload)
		return // handleWatch will close the connection
	}

	res := s.handler.HandleRequest(req)
	s.writeResponse(conn, res)
	conn.Close()
}

func (s *Server) writeResponse(conn net.Conn, res Response) {
	data, err := json.Marshal(res)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = conn.Write(data)
}

func (s *Server) handleWatch(conn net.Conn, payload []byte) {
	defer conn.Close()

	var p WatchPayload
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &p)
	}

	bus := s.handler.bus
	if bus == nil {
		s.writeResponse(conn, Response{OK: false, Error: "event bus not enabled"})
		return
	}

	// Send OK response to indicate stream is starting
	s.writeResponse(conn, Response{OK: true})

	// Initial State Dump
	// For each connected device, we emit device.connected and battery.update.
	devs := s.handler.devices.Connected()
	for _, dev := range devs {
		devData := map[string]interface{}{
			"id":   dev.ID(),
			"name": dev.Name(),
			"type": dev.Type,
		}

		// Send initial connected event
		initEv := map[string]interface{}{
			"type":      "device.connected",
			"deviceId":  dev.ID(),
			"timestamp": time.Now().UTC(),
			"payload":   devData,
		}
		data, _ := json.Marshal(initEv)
		data = append(data, '\n')
		if _, err := conn.Write(data); err != nil {
			return
		}

		// Send initial battery event
		charge, charging := dev.GetBattery()
		batEv := map[string]interface{}{
			"type":      "battery.update",
			"deviceId":  dev.ID(),
			"timestamp": time.Now().UTC(),
			"payload": map[string]interface{}{
				"charge":   charge,
				"charging": charging,
			},
		}
		data, _ = json.Marshal(batEv)
		data = append(data, '\n')
		if _, err := conn.Write(data); err != nil {
			return
		}
	}

	// Subscribe
	var filters []events.EventType
	for _, e := range p.Events {
		filters = append(filters, events.EventType(e))
	}

	sub := bus.Subscribe(filters...)
	defer sub.Close()

	for ev := range sub.C {
		data, err := json.Marshal(ev)
		if err != nil {
			continue
		}
		data = append(data, '\n')
		if _, err := conn.Write(data); err != nil {
			return // connection likely closed
		}
	}
}

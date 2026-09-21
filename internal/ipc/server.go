package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/log"
)

// Server handles listening for JSON IPC requests over a Unix socket.
type Server struct {
	path    string
	handler *Handler
	logger  log.Logger
}

// NewServer creates a new IPC server.
func NewServer(path string, handler *Handler, logger log.Logger) *Server {
	return &Server{
		path:    path,
		handler: handler,
		logger:  logger.With(log.String("component", "ipc")),
	}
}

// activatedListener adopts a Unix listener passed by systemd socket
// activation (fd 3+), or returns nil when not running socket-activated.
// Stdlib only: LISTEN_PID must match our pid and LISTEN_FDS must offer at
// least one socket. The adopted fd is dup'd by net.FileListener, so the
// *os.File wrapper is not retained.
func activatedListener() net.Listener {
	if os.Getenv("LISTEN_PID") != strconv.Itoa(os.Getpid()) {
		return nil
	}
	n, err := strconv.Atoi(os.Getenv("LISTEN_FDS"))
	if err != nil || n < 1 {
		return nil
	}
	f := os.NewFile(3, "kcd-socket-activated")
	l, err := net.FileListener(f)
	if err != nil {
		_ = f.Close()
		return nil
	}
	// Children (sshfs, notify-send, …) must not inherit the activation
	// environment and mistake it for sockets passed to them.
	_ = os.Unsetenv("LISTEN_PID")
	_ = os.Unsetenv("LISTEN_FDS")
	return l
}

// Listen starts listening on the Unix socket and processes incoming connections.
// When running under systemd socket activation with the default socket path,
// the passed listener is adopted instead of binding cfg.SocketPath.
func (s *Server) Listen(ctx context.Context) error {
	if s.path == config.DefaultSocketPath() {
		if l := activatedListener(); l != nil {
			return s.serve(ctx, l, true)
		}
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	// Always attempt to remove the old socket if it exists
	_ = os.Remove(s.path)

	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "unix", s.path)
	if err != nil {
		return err
	}

	// Restrict socket permissions to the current user (Fault Tolerance & Security Phase 2)
	if err := os.Chmod(s.path, 0600); err != nil {
		l.Close()
		return fmt.Errorf("failed to chmod ipc socket: %w", err)
	}

	return s.serve(ctx, l, false)
}

func (s *Server) serve(ctx context.Context, l net.Listener, activated bool) error {
	go func() {
		<-ctx.Done()
		l.Close()
		if !activated {
			os.Remove(s.path)
		}
	}()

	s.logger.Info("ipc server started",
		log.String("path", s.path),
		log.Bool("socket_activated", activated),
	)

	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			s.logger.Error("ipc socket accept error", log.Error(err))
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

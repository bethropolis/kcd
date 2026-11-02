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
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/netutil"
)

// Server handles listening for JSON IPC requests over a Unix socket.
type Server struct {
	path     string
	handler  *Handler
	logger   log.Logger
	readOnly bool
}

// NewReadOnlyServer creates a socket that rejects state-changing commands.
func NewReadOnlyServer(path string, handler *Handler, logger log.Logger) *Server {
	server := NewServer(path, handler, logger)
	server.readOnly = true
	return server
}

const (
	ipcReadDeadline      = 10 * time.Second
	ipcKeepAliveDeadline = 5 * time.Minute
	maxIPCLine           = 1 << 20
)

// NewServer creates a new IPC server.
func NewServer(path string, handler *Handler, logger log.Logger) *Server {
	return &Server{
		path:    path,
		handler: handler,
		logger:  logger.With(log.String("component", "ipc")),
	}
}

// activatedListener adopts a systemd-passed Unix listener (fd 3+), or nil
// when not socket-activated. The fd is dup'd, so the *os.File is dropped.
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

// Listen serves the Unix socket, adopting the systemd-passed listener when
// socket-activated instead of binding cfg.SocketPath.
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

	// Pin the socket to 0600: the process umask may be world-writable, and
	// the CLI has no other authentication layer.
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

	var acceptBackoff netutil.ErrorBackoff
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			s.logger.Warn("ipc socket accept error", log.Error(err))
			if !acceptBackoff.Wait(ctx) {
				return nil
			}
			continue
		}
		acceptBackoff.Reset()

		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(ipcReadDeadline))
	line, err := readIPCLine(reader)
	if err != nil {
		return
	}
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		s.writeResponse(conn, Err(CodeInvalidPayload, "malformed request", nil))
		return
	}
	if req.Command == CmdWatch {
		if s.readOnly && !watchIsReadOnly(req.Payload) {
			s.writeResponse(conn, Err(CodePermissionDenied, "read-only socket permits only non-opt-in watch events", nil))
			return
		}
		s.handleWatch(conn, req.Payload)
		return
	}

	keepAlive := false
	if req.Command == CmdHello {
		var payload HelloPayload
		if len(req.Payload) > 0 && json.Unmarshal(req.Payload, &payload) == nil {
			keepAlive = payload.KeepAlive
		}
	}
	var writeMu sync.Mutex
	write := func(request Request) {
		defer func() {
			if recover() != nil {
				s.writeResponseID(conn, Err(CodeInternal, "internal error", nil), request.ID, &writeMu)
			}
		}()
		if s.readOnly && !readOnlyCommand(request.Command) {
			s.writeResponseID(conn, Err(CodePermissionDenied, "command is not available on read-only socket", map[string]string{"command": request.Command}), request.ID, &writeMu)
			return
		}
		s.writeResponseID(conn, s.handler.HandleRequest(request), request.ID, &writeMu)
	}
	write(req)
	if !keepAlive {
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(ipcKeepAliveDeadline))
	var workers sync.WaitGroup
	for {
		line, err = readIPCLine(reader)
		if err != nil {
			break
		}
		var next Request
		if err := json.Unmarshal(line, &next); err != nil {
			s.writeResponseID(conn, Err(CodeInvalidPayload, "malformed request", nil), nil, &writeMu)
			break
		}
		if next.Command == CmdWatch {
			s.writeResponseID(conn, Err(CodeNotSupported, "watch is not supported on keep-alive connections", nil), next.ID, &writeMu)
			continue
		}
		workers.Add(1)
		go func(request Request) {
			defer workers.Done()
			write(request)
		}(next)
	}
	workers.Wait()
}

func readOnlyCommand(command string) bool {
	switch command {
	case CmdHello, CmdDevices, CmdStatus, CmdBattery, CmdConnectivity, CmdMprisStatus:
		return true
	default:
		return false
	}
}

func watchIsReadOnly(payload []byte) bool {
	var request WatchPayload
	if len(payload) > 0 && json.Unmarshal(payload, &request) != nil {
		return false
	}
	for _, typ := range request.Events {
		if typ == string(events.TypeSMSIncoming) || typ == string(events.TypeSMSAttachment) || typ == string(events.TypePrompt) {
			return false
		}
	}
	return true
}

func readIPCLine(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadBytes('\n')
	if len(line) > maxIPCLine {
		return nil, fmt.Errorf("ipc request exceeds %d bytes", maxIPCLine)
	}
	return line, err
}

func (s *Server) writeResponse(conn net.Conn, res Response) {
	s.writeResponseID(conn, res, nil, nil)
}

func (s *Server) writeResponseID(conn net.Conn, res Response, id json.RawMessage, mu *sync.Mutex) {
	res.ID = id
	data, err := json.Marshal(res)
	if err != nil {
		return
	}
	data = append(data, '\n')
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	_, _ = conn.Write(data)
}

package runcommand

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/device"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/protocol"
)

// PacketTypeOutput carries results to the phone's output card, advertising
// the streaming path: Android ignores the notification fallback by default.
const PacketTypeOutput = "kdeconnect.runcommand.output"

const (
	// streamFlushInterval bounds how long a line waits: the only knob trading
	// latency against packet count for chatty commands.
	streamFlushInterval = 250 * time.Millisecond

	// streamMaxLines caps a single execution's output. The phone keeps every
	// line in an unbounded Compose list, so the bound has to live here.
	streamMaxLines = 2000

	// streamMaxLineLen truncates a single pathological line (a minified
	// blob, a base64 dump) before it reaches the wire.
	streamMaxLineLen = 1024

	// streamChanBuffer absorbs a burst between flushes.
	streamChanBuffer = 256

	// streamMaxScanBuffer is the largest single line the scanner will read.
	// Anything longer is truncated by add rather than failing the scan.
	streamMaxScanBuffer = 1024 * 1024

	// streamNotifMaxBytes bounds the notification transcript. It is separate
	// from the streamed cap because a notification is a summary, not a log.
	streamNotifMaxBytes = 4000
)

// outputStream batches one execution's output, sending both lists every time:
// the phone's handler iterates them without a null check.
type outputStream struct {
	mu      sync.Mutex
	dev     device.Sender
	logger  log.Logger
	bus     *events.Bus
	id      int32
	command string

	stdout []string
	stderr []string
	total  int
	// notif accumulates a combined transcript for the notification fallback,
	// which has a much smaller budget than the streamed output.
	notif      []string
	notifBytes int
	// dropped records that the line cap was hit, so the final batch can say so
	// rather than silently ending early.
	dropped bool
	// announced records that the started event went out, so the paired
	// finished event is not published for an execution that never began.
	announced bool
	// stopped records that the execution was cancelled, so the finish packet
	// reports failure even if the process happened to exit zero.
	stopped bool
}

func (s *outputStream) add(isStderr bool, line string) {
	if len(line) > streamMaxLineLen {
		line = line[:streamMaxLineLen] + "..."
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.total >= streamMaxLines {
		s.dropped = true
		return
	}
	s.total++
	if isStderr {
		s.stderr = append(s.stderr, line)
	} else {
		s.stdout = append(s.stdout, line)
	}

	// The notification has its own much smaller budget, so it tracks bytes
	// rather than lines and stops growing once full.
	if s.notifBytes < streamNotifMaxBytes {
		s.notif = append(s.notif, line)
		s.notifBytes += len(line) + 1
	}
}

// summary renders the notification transcript, stdout and stderr interleaved
// in arrival order.
func (s *outputStream) summary() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	text := strings.Join(s.notif, "\n")
	if s.dropped {
		text += "\n...[output truncated]"
	}
	return text
}

// pending reports whether anything is buffered, so the caller can skip
// sending an empty batch.
func (s *outputStream) pending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.stdout) > 0 || len(s.stderr) > 0
}

// take swaps out the buffered lines, leaving empty slices behind. Both keys
// are always present in the packet even when one is empty.
func (s *outputStream) take() (stdout, stderr []string, dropped bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stdout, stderr, dropped = s.stdout, s.stderr, s.dropped
	s.stdout, s.stderr = nil, nil
	return stdout, stderr, dropped
}

// send emits one commandOutput packet.
func (s *outputStream) send(stdout, stderr []string, dropped bool) {
	if len(stdout) == 0 {
		stdout = []string{}
	}
	if len(stderr) == 0 {
		stderr = []string{}
	}
	if dropped {
		stderr = append(stderr, "[output truncated: over 2000 lines]")
	}

	body := map[string]any{
		"commandOutput": true,
		"id":            s.id,
		"stdout":        stdout,
		"stderr":        stderr,
	}
	pkt, err := protocol.NewPacket(PacketTypeOutput, body)
	if err != nil {
		s.logger.Warn("runcommand: build output packet", log.Error(err))
		return
	}
	if err := s.dev.Send(pkt); err != nil {
		s.logger.Warn("runcommand: send output", log.Error(err), log.Int("id", int(s.id)))
	}
}

// flush emits buffered lines to subscribers only (idle/unwatched commands
// stay silent); lifecycle events stay unconditional so outcomes still land.
func (s *outputStream) flush() {
	if !s.pending() {
		return
	}
	stdout, stderr, dropped := s.take()
	s.send(stdout, stderr, dropped)

	if s.bus == nil || !s.bus.HasSubscribers(events.TypeRunCommandOutput) {
		return
	}
	s.bus.Publish(events.TypeRunCommandOutput, s.dev.ID(), map[string]any{
		"id":        s.id,
		"key":       s.command,
		"status":    "output",
		"stdout":    stdout,
		"stderr":    stderr,
		"truncated": dropped,
	})
}

// publishLifecycle announces a start or finish. Unconditional: these are two
// events per command and carry the result a client needs most.
func (s *outputStream) publishLifecycle(status string, extra map[string]any) {
	if s.bus == nil {
		return
	}
	payload := map[string]any{"id": s.id, "key": s.command, "status": status}
	for k, v := range extra {
		payload[k] = v
	}
	s.bus.Publish(events.TypeRunCommandOutput, s.dev.ID(), payload)
}

// line is one scanned line tagged with the stream it came from.
type line struct {
	text     string
	isStderr bool
}

// streamOutput reports output in started/output/finished order (the phone
// keys rows off the started id), deferring finish so panics still release it.
func (p *RunCommandPlugin) streamOutput(
	ctx context.Context,
	dev device.Sender,
	id int32,
	key string,
	cmd *exec.Cmd,
) string {
	var stream *outputStream
	// The finish packet reports the outcome, so it is always sent exactly
	// once, including on the early-return paths below.
	var success bool
	// summary is filled in on the normal exit path and read by the deferred
	// publish, so the transcript is rendered once rather than twice.
	var summary string
	defer func() {
		if stream != nil && stream.stopped {
			success = false
		}
		// Only for an execution that actually started, so a client never sees
		// a finish with no matching start.
		if stream != nil && stream.announced {
			stream.publishLifecycle("finished", map[string]any{
				"success": success,
				"output":  summary,
			})
		}
		p.finishOutput(dev, id, success)
	}()

	// Built before the pipes exist so deferred finish finds it on early
	// returns; announced stays false until running, so failed starts publish nothing.
	stream = &outputStream{dev: dev, logger: p.logger, bus: p.bus, id: id, command: key}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		p.logger.Warn("runcommand: stdout pipe", log.Error(err))
		return ""
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		p.logger.Warn("runcommand: stderr pipe", log.Error(err))
		return ""
	}
	if err := cmd.Start(); err != nil {
		p.logger.Warn("runcommand: start", log.Error(err))
		return ""
	}
	stream.announced = true
	stream.publishLifecycle("started", nil)

	// exec kills only the direct child: a forking shell's orphan would hold
	// the pipes open forever, so close our read ends when the context ends.
	scanDone := make(chan struct{})
	defer close(scanDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = stdoutPipe.Close()
			_ = stderrPipe.Close()
		case <-scanDone:
		}
	}()

	lines := make(chan line, streamChanBuffer)
	var readers sync.WaitGroup
	readers.Add(2)
	go func() { defer readers.Done(); scanInto(lines, stdoutPipe, false, p.logger) }()
	go func() { defer readers.Done(); scanInto(lines, stderrPipe, true, p.logger) }()
	go func() {
		readers.Wait()
		close(lines)
	}()

	ticker := time.NewTicker(streamFlushInterval)
	defer ticker.Stop()

	// Run until both pipes EOF rather than returning on ctx: scanners would
	// otherwise block forever on a send nobody reads after cancellation.
	for {
		closed := drainLines(lines, stream)
		stream.flush()
		if closed {
			break
		}

		select {
		case l, ok := <-lines:
			if ok {
				stream.add(l.isStderr, l.text)
			}
		case <-ticker.C:
			stream.flush()
		case <-ctx.Done():
			// The process is being killed; keep draining until EOF so the
			// last lines are not lost, then report failure.
			stream.stopped = true
		}
	}

	readers.Wait()
	waitErr := cmd.Wait()
	success = waitErr == nil

	summary = stream.summary()
	return summary
}

// drainLines consumes the buffer, reporting closed-and-empty; draining first
// collapses a burst into one packet instead of one per line.
func drainLines(lines <-chan line, s *outputStream) (closed bool) {
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				return true
			}
			s.add(l.isStderr, l.text)
		default:
			return false
		}
	}
}

// scanInto splits a pipe into lines with blocking sends, so a slow consumer
// backpressures the command; the line cap in add is the bound.
func scanInto(out chan<- line, r io.Reader, isStderr bool, logger log.Logger) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), streamMaxScanBuffer)
	for scanner.Scan() {
		out <- line{text: scanner.Text(), isStderr: isStderr}
	}
	if err := scanner.Err(); err != nil {
		logger.Debug("runcommand: output scan ended", log.Error(err))
	}
}

// finishOutput emits commandFinished and clears the registration; the phone
// colors the line off it, so exactly once per started execution.
func (p *RunCommandPlugin) finishOutput(dev device.Sender, id int32, success bool) {
	p.Mu.Lock()
	if byID, ok := p.running[dev.ID()]; ok {
		delete(byID, id)
		if len(byID) == 0 {
			delete(p.running, dev.ID())
		}
	}
	p.Mu.Unlock()

	body := map[string]any{
		"commandFinished": true,
		"id":              id,
		"success":         success,
	}
	pkt, err := protocol.NewPacket(PacketTypeOutput, body)
	if err != nil {
		p.logger.Warn("runcommand: build finished packet", log.Error(err))
		return
	}
	if err := dev.Send(pkt); err != nil {
		p.logger.Warn("runcommand: send finished", log.Error(err), log.Int("id", int(id)))
	}
}

// nextExecID hands out execution ids as a counter: the phone's getInt would
// overflow on nanosecond timestamps.
func (p *RunCommandPlugin) nextExecID() int32 {
	p.Mu.Lock()
	defer p.Mu.Unlock()
	p.execSeq++
	return p.execSeq
}

// stopRunning cancels a running execution, if the phone asked for it. The
// phone's stop button sends kdeconnect.runcommand.request {"stop": true}.
func (p *RunCommandPlugin) stopRunning(deviceID string, id int32) {
	p.Mu.Lock()
	cancel := p.running[deviceID][id]
	p.Mu.Unlock()

	if cancel == nil {
		p.logger.Debug("runcommand: stop for unknown execution",
			log.String("device_id", deviceID), log.Int("id", int(id)))
		return
	}
	p.logger.Info("runcommand: stopping execution on request",
		log.String("device_id", deviceID), log.Int("id", int(id)))
	cancel()
}

// stopAll cancels everything running for a device, used when it disconnects.
func (p *RunCommandPlugin) stopAll(deviceID string) {
	p.Mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(p.running[deviceID]))
	for _, cancel := range p.running[deviceID] {
		cancels = append(cancels, cancel)
	}
	delete(p.running, deviceID)
	p.Mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
}

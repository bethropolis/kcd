// Package hooks runs user-configured commands in response to daemon events.
package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/internal/log"
	"github.com/bethropolis/kcd/internal/sysexec"
)

const (
	maxConcurrent  = 4
	defaultTimeout = 10 * time.Second
)

// Runner owns hook subscriptions and bounded command execution.
type Runner struct {
	hooks map[string]config.HookConfig
	bus   *events.Bus
	exec  *sysexec.Exec
	log   log.Logger
	mu    sync.Mutex
	last  map[string]time.Time
}

// New creates a hook runner. A nil hook map disables hooks without work.
func New(hooks map[string]config.HookConfig, bus *events.Bus, logger log.Logger) *Runner {
	cpy := make(map[string]config.HookConfig, len(hooks))
	for event, hook := range hooks {
		cpy[event] = hook
	}
	return &Runner{hooks: cpy, bus: bus, exec: sysexec.New(nil), log: logger.With(log.String("component", "hooks")), last: make(map[string]time.Time)}
}

// Start subscribes to exactly the configured event types and returns.
func (r *Runner) Start(ctx context.Context) {
	if len(r.hooks) == 0 || r.bus == nil {
		return
	}
	filters := make([]events.EventType, 0, len(r.hooks))
	for event := range r.hooks {
		filters = append(filters, events.EventType(event))
	}
	sub := r.bus.Subscribe(events.WatchSubscriberCap, filters...)
	go func() {
		defer sub.Close()
		semaphore := make(chan struct{}, maxConcurrent)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-sub.C:
				if !ok {
					return
				}
				select {
				case semaphore <- struct{}{}:
					go func(event events.Event) {
						defer func() { <-semaphore }()
						r.run(ctx, event)
					}(event)
				default:
					r.log.Warn("hook worker saturated", log.String("event", string(event.Type)))
				}
			}
		}
	}()
}

func (r *Runner) run(parent context.Context, event events.Event) {
	hook, ok := r.hooks[string(event.Type)]
	if !ok || !matchesDevice(hook.Devices, event.DeviceID) || r.debounced(event, hook.Debounce) {
		return
	}
	command := expandCommand(hook.Command, event)
	if len(command) == 0 || command[0] == "" {
		return
	}
	payload, err := json.Marshal(event)
	if err != nil {
		r.log.Warn("hook event encode failed", log.String("event", string(event.Type)), log.Error(err))
		return
	}
	timeout := time.Duration(hook.Timeout)
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), timeout)
	defer cancel()
	env := []string{
		fmt.Sprintf("KCD_EVENT_TYPE=%s", event.Type),
		fmt.Sprintf("KCD_DEVICE_ID=%s", event.DeviceID),
	}
	if _, err := r.exec.RunInputEnv(ctx, string(payload), command[0], env, command[1:]...); err != nil {
		r.log.Warn("hook command failed", log.String("event", string(event.Type)), log.String("command", command[0]), log.Error(err))
	}
}

func (r *Runner) debounced(event events.Event, duration config.FlexDuration) bool {
	if duration <= 0 {
		return false
	}
	key := string(event.Type) + "\x00" + event.DeviceID
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if previous, ok := r.last[key]; ok && now.Sub(previous) < time.Duration(duration) {
		return true
	}
	r.last[key] = now
	return false
}

func matchesDevice(devices []string, deviceID string) bool {
	if len(devices) == 0 {
		return true
	}
	for _, allowed := range devices {
		if allowed == deviceID {
			return true
		}
	}
	return false
}

func expandCommand(command []string, event events.Event) []string {
	result := make([]string, len(command))
	for i, arg := range command {
		arg = strings.ReplaceAll(arg, "{event.type}", string(event.Type))
		arg = strings.ReplaceAll(arg, "{device.id}", event.DeviceID)
		if strings.Contains(arg, "{payload.") {
			arg = replacePayload(arg, event.Payload)
		}
		if strings.HasPrefix(arg, "~/") {
			// User expansion is deliberately omitted here: commands remain literal
			// argv and are resolved by the user's shell/tool environment.
		}
		result[i] = arg
	}
	return result
}

func replacePayload(text string, payload any) string {
	for start := strings.Index(text, "{payload."); start >= 0; {
		end := strings.IndexByte(text[start:], '}')
		if end < 0 {
			break
		}
		end += start
		key := text[start+9 : end]
		value := ""
		if object, ok := payload.(map[string]any); ok {
			if item, found := object[key]; found {
				value = stringify(item)
			}
		}
		text = text[:start] + value + text[end+1:]
		start = strings.Index(text, "{payload.")
	}
	return text
}

func stringify(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

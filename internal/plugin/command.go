package plugin

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// GetDBusEnv attempts to find the session bus address.
// Systemd user services sometimes lack this variable, which causes D-Bus
// based CLI tools (like playerctl) to core dump or fail.
func GetDBusEnv() []string {
	env := os.Environ()
	hasDBus := false
	for _, e := range env {
		if strings.HasPrefix(e, "DBUS_SESSION_BUS_ADDRESS=") {
			hasDBus = true
			break
		}
	}
	if !hasDBus {
		// Fallback for typical Linux desktop setups where the bus is at a predictable path
		uid := os.Getuid()
		busPath := fmt.Sprintf("/run/user/%d/bus", uid)
		if _, err := os.Stat(busPath); err == nil {
			env = append(env, fmt.Sprintf("DBUS_SESSION_BUS_ADDRESS=unix:path=%s", busPath))
		}
	}
	return env
}

// NewPlayerctlCmd creates an exec.Cmd for playerctl with the correct environment.
func NewPlayerctlCmd(ctx context.Context, args ...string) *exec.Cmd {
	var cmd *exec.Cmd
	if ctx != nil {
		cmd = exec.CommandContext(ctx, "playerctl", args...)
	} else {
		cmd = exec.Command("playerctl", args...)
	}
	cmd.Env = GetDBusEnv()
	return cmd
}

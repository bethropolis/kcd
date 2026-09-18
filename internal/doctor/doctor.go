// Package doctor performs runtime dependency and configuration checks.
package doctor

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"

	"github.com/bethropolis/kcd/internal/config"
	"github.com/bethropolis/kcd/internal/protocol"
)

// Check is the result of a single diagnostic check.
type Check struct {
	Name   string
	Detail string // shown on failure
	Pass   bool
}

// Run performs all checks and returns their results in order.
func Run() []Check {
	checks := []Check{}

	// daemon running
	daemonCheck := checkDaemon()
	checks = append(checks, daemonCheck)

	// notify-send
	checks = append(checks, checkBin("notify-send", "notify-send", "install libnotify-bin"))

	// wl-copy / xclip
	checks = append(checks, checkAny("wl-copy / xclip",
		"install wl-clipboard (Wayland) or xclip (X11)",
		"wl-copy", "xclip"))

	// sshfs
	checks = append(checks, checkBin("sshfs", "sshfs", "install sshfs"))

	// ydotool / xdotool
	checks = append(checks, checkAny("ydotool / xdotool",
		"install ydotool (Wayland) or xdotool (X11)",
		"ydotool", "xdotool"))

	// wtype (Wayland text input for mousepad plugin)
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		checks = append(checks, checkBin("wtype", "wtype", "install wtype for Wayland keyboard input"))
	}

	// port <tcp_port>/udp open (configured tcp_port, 1716 by default)
	tcpPort := configuredTCPPort()
	checks = append(checks, checkUDPPort(daemonCheck.Pass, tcpPort))

	// port <tcp_port>/tcp open
	checks = append(checks, checkTCPPort(daemonCheck.Pass, tcpPort))

	// config file readable
	checks = append(checks, checkConfigFile())

	// cert file exists
	checks = append(checks, checkCertFile())

	return checks
}

// configuredTCPPort returns tcp_port from the on-disk config, falling back
// to the protocol default when the config is missing or unset.
func configuredTCPPort() int {
	cfg, err := config.Load(config.DefaultConfigPath())
	if err != nil || cfg.TCPPort == 0 {
		return protocol.DefaultTCPPort
	}
	return cfg.TCPPort
}

func checkDaemon() Check {
	socketPath := config.DefaultSocketPath()
	dialer := net.Dialer{Timeout: 1 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err == nil {
		conn.Close()
		return Check{Name: "daemon running", Pass: true}
	}
	return Check{Name: "daemon running", Detail: "daemon not reachable at " + socketPath, Pass: false}
}

func checkBin(name, bin, hint string) Check {
	if _, err := exec.LookPath(bin); err == nil {
		return Check{Name: name, Pass: true}
	}
	return Check{Name: name, Detail: hint, Pass: false}
}

func checkAny(name, hint string, bins ...string) Check {
	for _, b := range bins {
		if _, err := exec.LookPath(b); err == nil {
			return Check{Name: name, Pass: true}
		}
	}
	return Check{Name: name, Detail: hint, Pass: false}
}

func checkUDPPort(daemonRunning bool, port int) Check {
	name := fmt.Sprintf("port %d/udp open", port)
	if daemonRunning {
		return Check{Name: name, Pass: true}
	}
	var lc net.ListenConfig
	l, err := lc.ListenPacket(context.Background(), "udp", fmt.Sprintf(":%d", port))
	if err != nil {
		return Check{Name: name, Detail: "port is blocked or in use: " + err.Error(), Pass: false}
	}
	l.Close()
	return Check{Name: name, Pass: true}
}

func checkTCPPort(daemonRunning bool, port int) Check {
	name := fmt.Sprintf("port %d/tcp open", port)
	if daemonRunning {
		return Check{Name: name, Pass: true}
	}
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return Check{Name: name, Detail: "port is blocked or in use: " + err.Error(), Pass: false}
	}
	l.Close()
	return Check{Name: name, Pass: true}
}

func checkConfigFile() Check {
	path := config.DefaultConfigPath()
	if _, err := os.Stat(path); err == nil {
		return Check{Name: "config file readable", Pass: true}
	}
	return Check{Name: "config file readable", Detail: "not found at " + path + " (using defaults)", Pass: false}
}

func checkCertFile() Check {
	cfg, err := config.Load(config.DefaultConfigPath())
	if err != nil {
		return Check{Name: "cert file exists", Detail: "could not load config: " + err.Error(), Pass: false}
	}
	if _, err := os.Stat(cfg.CertFile); err == nil {
		return Check{Name: "cert file exists", Pass: true}
	}
	return Check{Name: "cert file exists", Detail: "not found at " + cfg.CertFile + " (will be generated on first daemon start)", Pass: false}
}

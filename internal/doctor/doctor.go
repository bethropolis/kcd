// Package doctor performs runtime dependency and configuration checks.
package doctor

import (
	"net"
	"os"
	"os/exec"
	"time"

	"github.com/bethropolis/kcd/internal/config"
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
	checks = append(checks, checkDaemon())

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

	// port 1716/udp open
	checks = append(checks, checkUDPPort())

	// port 1716/tcp open
	checks = append(checks, checkTCPPort())

	// config file readable
	checks = append(checks, checkConfigFile())

	// cert file exists
	checks = append(checks, checkCertFile())

	return checks
}

func checkDaemon() Check {
	socketPath := config.DefaultSocketPath()
	conn, err := net.DialTimeout("unix", socketPath, 1*time.Second)
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

func checkUDPPort() Check {
	l, err := net.ListenPacket("udp", ":1716")
	if err != nil {
		return Check{Name: "port 1716/udp open", Detail: "port is blocked or in use: " + err.Error(), Pass: false}
	}
	l.Close()
	return Check{Name: "port 1716/udp open", Pass: true}
}

func checkTCPPort() Check {
	l, err := net.Listen("tcp", ":1716")
	if err != nil {
		return Check{Name: "port 1716/tcp open", Detail: "port is blocked or in use: " + err.Error(), Pass: false}
	}
	l.Close()
	return Check{Name: "port 1716/tcp open", Pass: true}
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

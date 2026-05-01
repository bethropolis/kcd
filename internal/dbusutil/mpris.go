// Package dbusutil provides shared helpers for kcd plugins.
package dbusutil

import (
	"os/exec"
	"strings"

	"go.uber.org/zap"
)

// PauseMPRIS sends Pause to all active MPRIS media players via playerctl.
// Returns the list of player names that were paused so Play can be sent later.
func PauseMPRIS(logger *zap.Logger) []string {
	out, err := exec.Command("playerctl", "-l").Output()
	if err != nil {
		return nil
	}
	players := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(players) == 0 || (len(players) == 1 && players[0] == "") {
		return nil
	}

	if err := exec.Command("playerctl", "pause", "-a").Run(); err != nil {
		logger.Debug("dbusutil: global pause failed", zap.Error(err))
	}

	return players
}

// PlayMPRIS sends Play to the named MPRIS players via playerctl.
func PlayMPRIS(players []string, logger *zap.Logger) {
	for _, p := range players {
		if p == "" {
			continue
		}
		if err := exec.Command("playerctl", "-p", p, "play").Run(); err != nil {
			logger.Debug("dbusutil: play failed", zap.String("player", p), zap.Error(err))
		}
	}
}

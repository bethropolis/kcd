package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12] + "…"
	}
	return id
}

func formatMs(ms int64) string {
	if ms < 0 {
		return "??:??"
	}
	totalSec := ms / 1000
	min := totalSec / 60
	sec := totalSec % 60
	return fmt.Sprintf("%d:%02d", min, sec)
}

// parseSeek parses a seek offset string into milliseconds.
// Supported formats: +30s, -10s, 1m30s, 45 (bare seconds).
func parseSeek(s string) (int64, error) {
	if strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") {
		isNeg := strings.HasPrefix(s, "-")
		rest := s[1:]
		d, err := parseDuration(rest)
		if err != nil {
			return 0, fmt.Errorf("invalid offset %q", s)
		}
		if isNeg {
			return -d, nil
		}
		return d, nil
	}
	d, err := parseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid offset %q", s)
	}
	return d, nil
}

func parseDuration(s string) (int64, error) {
	d, err := time.ParseDuration(s)
	if err == nil {
		return int64(d.Milliseconds()), nil
	}
	// Try bare seconds
	if secs, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(secs * 1000), nil
	}
	return 0, fmt.Errorf("cannot parse %q as duration", s)
}

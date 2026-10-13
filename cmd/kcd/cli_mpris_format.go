package main

import (
	"errors"
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

// errSeekHelp signals that SkipFlagParsing kept the help flag from being
// handled, so the caller shows the command's help itself.
var errSeekHelp = errors.New("seek: help requested")

// seekArgs is the parsed argument list of `kcd mpris seek`.
type seekArgs struct {
	device string
	player string
	offset string
}

// splitSeekArgs recovers --device/--player and the offset from raw args. A
// "-" followed by a digit is always the offset; anything else errors out.
func splitSeekArgs(args []string) (seekArgs, error) {
	var out seekArgs
	var offset string
	flagsDone := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case flagsDone || arg == "":
			offset = firstNonEmpty(offset, arg)
		case arg == "--":
			flagsDone = true
		case arg == "-h" || arg == "--help":
			return out, errSeekHelp
		case arg == "-p" || arg == "--player":
			v, ok := nextValue(args, &i)
			if !ok {
				return out, fmt.Errorf("seek: %s requires a value", arg)
			}
			out.player = v
		case strings.HasPrefix(arg, "--player="):
			out.player = strings.TrimPrefix(arg, "--player=")
		case isNegativeOffset(arg):
			offset = firstNonEmpty(offset, arg)
		case arg == "--device":
			v, ok := nextValue(args, &i)
			if !ok {
				return out, fmt.Errorf("seek: --device requires a value")
			}
			out.device = v
		case strings.HasPrefix(arg, "--device="):
			out.device = strings.TrimPrefix(arg, "--device=")
		case strings.HasPrefix(arg, "-"):
			return out, fmt.Errorf("seek: unknown flag %q (use %q alone, or -- -10s, for a backward seek)", arg, "kcd mpris seek -10s")
		default:
			offset = firstNonEmpty(offset, arg)
		}
	}

	if offset == "" {
		return out, fmt.Errorf("seek: missing offset argument")
	}
	out.offset = offset
	return out, nil
}

// isNegativeOffset reports whether arg is a negative offset like "-10s": a
// "-" followed by a digit.
func isNegativeOffset(arg string) bool {
	if len(arg) < 2 || arg[0] != '-' {
		return false
	}
	return arg[1] >= '0' && arg[1] <= '9'
}

func nextValue(args []string, i *int) (string, bool) {
	if *i+1 >= len(args) {
		return "", false
	}
	*i++
	return args[*i], true
}

func firstNonEmpty(cur, v string) string {
	if cur != "" {
		return cur
	}
	return v
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

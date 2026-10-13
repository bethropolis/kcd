package log

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest"
	"go.uber.org/zap/zaptest/observer"
)

// NewDevelopment returns a human-readable logger for tests.
func NewDevelopment() Logger {
	zl, _ := zap.NewDevelopment(zap.AddCallerSkip(1))
	return Logger{zap: zl, level: zap.NewAtomicLevel()}
}

// NewTest returns a Logger wired to the given test's output.
func NewTest(t *testing.T) Logger {
	return Logger{zap: zaptest.NewLogger(t, zaptest.WrapOptions(zap.AddCallerSkip(1))), level: zap.NewAtomicLevel()}
}

// Observe returns a debug Logger plus a snapshot of captured entries, for
// asserting what reaches the log — depguard keeps zap imports out of tests.
func Observe() (Logger, func() []string) {
	level := zap.NewAtomicLevel()
	level.SetLevel(zapcore.DebugLevel)
	core, logs := observer.New(level)
	l := Logger{zap: zap.New(core, zap.AddCallerSkip(1)), level: level}

	return l, func() []string {
		entries := logs.All()
		out := make([]string, 0, len(entries))
		for _, e := range entries {
			var b strings.Builder
			b.WriteString(e.Message)
			fields := e.ContextMap()
			keys := make([]string, 0, len(fields))
			for k := range fields {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprint(&b, " ", k, "=", fields[k])
			}
			out = append(out, b.String())
		}
		return out
	}
}

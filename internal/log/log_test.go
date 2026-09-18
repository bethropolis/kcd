package log

import (
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestNewLevelNames(t *testing.T) {
	for _, level := range []string{"debug", "info", "warn", "error", "quiet", "bogus"} {
		l, err := New(level)
		if err != nil {
			t.Fatalf("New(%q) returned error: %v", level, err)
		}
		_ = l.Sync()
	}
}

func TestNopDiscards(t *testing.T) {
	l := Nop()
	l.Debug("d", String("k", "v"))
	l.Info("i", Int("n", 1))
	l.Warn("w", Bool("b", true))
	l.Error("e", Error(nil))
	_ = l.With(String("k", "v")).Named("n")
	l.SetLevel("debug")
	_ = l.Sync()
	// Fatal intentionally not exercised: it exits the process.
}

func newObservedLogger() (Logger, *observer.ObservedLogs) {
	// Mirror production wiring: the Logger's atomic level gates the core.
	atomic := zap.NewAtomicLevel()
	atomic.SetLevel(zapcore.DebugLevel)
	core, logged := observer.New(atomic)
	l := Logger{zap: zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1)), level: atomic}
	return l, logged
}

func TestCallerPointsAtCallSite(t *testing.T) {
	l, logged := newObservedLogger()

	l.Info("direct", String("k", "v"))
	l.With(String("a", "b")).Warn("scoped")
	l.Named("test").Error("named", Int64("n", 1), Duration("d", time.Second))

	entries := logged.All()
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	for _, e := range entries {
		if !e.Caller.Defined {
			t.Error("entry has no caller information")
			continue
		}
		if !strings.HasSuffix(e.Caller.File, "log_test.go") {
			t.Errorf("caller = %s, want this test file (wrapper leaked into caller attribution)", e.Caller.File)
		}
	}
}

func TestSetLevelFilters(t *testing.T) {
	l, logged := newObservedLogger()

	l.SetLevel("error")
	l.Info("dropped")
	l.Error("kept")

	if logged.Len() != 1 {
		t.Fatalf("got %d entries after SetLevel(error), want 1", logged.Len())
	}
	if got := logged.All()[0].Message; got != "kept" {
		t.Errorf("surviving entry = %q, want %q", got, "kept")
	}
}

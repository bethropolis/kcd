// Package log is kcd's single logging seam: every package logs through it
// instead of zap directly, so backend concerns live in exactly one place.
package log

import "go.uber.org/zap"

// Logger is a structured logger. The zero value is not usable;
// construct via New, Nop, NewDevelopment, or NewTest.
type Logger struct {
	zap   *zap.Logger
	level zap.AtomicLevel
}

// Debug, Info, Warn and Error are level-forwards to the zap backend. They
// exist so callers never depend on zap directly (see the package comment).
func (l Logger) Debug(msg string, fields ...Field) { l.zap.Debug(msg, fields...) }

func (l Logger) Info(msg string, fields ...Field) { l.zap.Info(msg, fields...) }

func (l Logger) Warn(msg string, fields ...Field) { l.zap.Warn(msg, fields...) }

func (l Logger) Error(msg string, fields ...Field) { l.zap.Error(msg, fields...) }

// Fatal logs at fatal level, then exits. Retained for parity with the
// daemon's startup path; plugins must never call it.
func (l Logger) Fatal(msg string, fields ...Field) { l.zap.Fatal(msg, fields...) }

// With returns a Logger carrying additional context fields.
func (l Logger) With(fields ...Field) Logger {
	return Logger{zap: l.zap.With(fields...), level: l.level}
}

// Named adds a logger name segment (surfaced as "logger" in output).
func (l Logger) Named(name string) Logger {
	return Logger{zap: l.zap.Named(name), level: l.level}
}

// SetLevel adjusts verbosity at runtime using the same names as New.
// See level.go.
func (l Logger) SetLevel(level string) { setAtomicLevel(l.level, level) }

// Sync flushes buffered output. Whoever owns the root logger defers it.
func (l Logger) Sync() error { return l.zap.Sync() }

// Nop returns a Logger that discards everything. Used in tests and as
// a fallback where no logger was provided.
func Nop() Logger {
	return Logger{zap: zap.NewNop(), level: zap.NewAtomicLevel()}
}

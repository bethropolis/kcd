package log

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
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

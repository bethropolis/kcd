package log

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New builds the daemon logger for a config level name: "debug",
// "info", "warn", "error" (or "quiet"). Unknown names fall back to
// info. Debug selects the development encoder; anything else uses
// production. The returned Logger carries an atomic level so SetLevel
// can adjust verbosity at runtime without restarting.
//
// AddCallerSkip keeps the "caller" field pointing at the real call
// site instead of this wrapper; With and Named inherit it.
func New(level string) (Logger, error) {
	atomic := zap.NewAtomicLevel()
	setAtomicLevel(atomic, level)

	var cfg zap.Config
	if level == "debug" {
		cfg = zap.NewDevelopmentConfig()
	} else {
		cfg = zap.NewProductionConfig()
	}
	cfg.Level = atomic

	zl, err := cfg.Build(zap.AddCallerSkip(1))
	if err != nil {
		return Logger{}, err
	}
	return Logger{zap: zl, level: atomic}, nil
}

func setAtomicLevel(al zap.AtomicLevel, level string) {
	switch level {
	case "debug":
		al.SetLevel(zapcore.DebugLevel)
	case "warn":
		al.SetLevel(zapcore.WarnLevel)
	case "error", "quiet":
		al.SetLevel(zapcore.ErrorLevel)
	default:
		al.SetLevel(zapcore.InfoLevel)
	}
}

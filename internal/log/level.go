package log

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New builds the daemon logger for a config level name. Unknown names fall
// back to info; AddCallerSkip keeps "caller" pointing at the real call site.
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

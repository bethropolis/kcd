package log

import (
	"time"

	"go.uber.org/zap"
)

// Field is a structured log field. A direct alias of zap.Field, so
// there is zero conversion cost at call sites.
type Field = zap.Field

// String adds a string field.
func String(key, val string) Field { return zap.String(key, val) }

// Strings adds a string-slice field.
func Strings(key string, val []string) Field { return zap.Strings(key, val) }

// Int adds an int field.
func Int(key string, val int) Field { return zap.Int(key, val) }

// Int64 adds an int64 field.
func Int64(key string, val int64) Field { return zap.Int64(key, val) }

// Uint64 adds a uint64 field.
func Uint64(key string, val uint64) Field { return zap.Uint64(key, val) }

// Bool adds a bool field.
func Bool(key string, val bool) Field { return zap.Bool(key, val) }

// Float64 adds a float64 field.
func Float64(key string, val float64) Field { return zap.Float64(key, val) }

// Duration adds a time.Duration field.
func Duration(key string, val time.Duration) Field { return zap.Duration(key, val) }

// Any adds a field using reflection for the value.
func Any(key string, val any) Field { return zap.Any(key, val) }

// Error adds an error field under the "error" key. A nil error is skipped.
func Error(err error) Field { return zap.Error(err) }

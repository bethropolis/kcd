//go:build !linux

package discovery

import "context"

// WatchNetworkChanges is unavailable on this platform; timer fallback remains active.
func WatchNetworkChanges(context.Context, func()) {}

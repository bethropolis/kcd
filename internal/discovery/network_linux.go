//go:build linux

package discovery

import (
	"context"

	"golang.org/x/sys/unix"
)

// WatchNetworkChanges invokes onChange for local address and link changes.
// The blocking netlink read has no timer or polling cost while idle.
func WatchNetworkChanges(ctx context.Context, onChange func()) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return
	}
	defer unix.Close(fd)
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK, Groups: unix.RTMGRP_IPV4_IFADDR | unix.RTMGRP_IPV6_IFADDR | unix.RTMGRP_LINK}); err != nil {
		return
	}

	go func() {
		<-ctx.Done()
		_ = unix.Close(fd)
	}()
	buffer := make([]byte, 4096)
	for {
		if _, err := unix.Read(fd, buffer); err != nil {
			return
		}
		if onChange != nil {
			onChange()
		}
	}
}

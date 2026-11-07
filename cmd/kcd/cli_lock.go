package main

import (
	"github.com/bethropolis/kcd/pkg/client"
)

var lockCmd = deviceActionCmd("lock", "Lock the screen of a remote device", "Lock requested", func(cl *client.Client, id string) error {
	return cl.Lock(id)
})

package main

import (
	"github.com/bethropolis/kcd/pkg/client"
)

var unlockCmd = deviceActionCmd("unlock", "Unlock the screen of a remote device", "Unlock requested", func(cl *client.Client, id string) error {
	return cl.Unlock(id)
})

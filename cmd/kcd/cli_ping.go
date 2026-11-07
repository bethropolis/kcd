package main

import (
	"github.com/bethropolis/kcd/pkg/client"
)

var pingCmd = deviceActionCmd("ping", "Send a ping notification to a device", "Ping sent", func(cl *client.Client, id string) error {
	return cl.Ping(id)
})

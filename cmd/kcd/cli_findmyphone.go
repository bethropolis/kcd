package main

import (
	"github.com/bethropolis/kcd/pkg/client"
)

var findmyphoneCmd = deviceActionCmd("findmyphone", "Make the phone play a loud ringtone", "Ring request sent", func(cl *client.Client, id string) error {
	return cl.FindMyPhone(id)
})

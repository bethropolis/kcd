package client_test

import (
	"context"
	"fmt"
	"time"

	"github.com/bethropolis/kcd/internal/events"
	"github.com/bethropolis/kcd/pkg/client"
)

func ExampleClient_Watch() {
	c := client.New("/run/user/1000/kcd/kcd.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch := make(chan events.Event, 10)
	go func() {
		for ev := range ch {
			fmt.Printf("event: %s from %s\n", ev.Type, ev.DeviceID)
		}
	}()

	_ = c.Watch(ctx, []string{"battery.update"}, ch)
}

func ExampleClient_ShareFile() {
	c := client.New("/run/user/1000/kcd/kcd.sock")
	err := c.ShareFile("remote-device-id", "/home/user/Pictures/photo.jpg")
	if err != nil {
		fmt.Printf("share failed: %v\n", err)
	}
}

func ExampleClient_Pair() {
	c := client.New("/run/user/1000/kcd/kcd.sock")
	pin, err := c.Pair("remote-device-id")
	if err != nil {
		fmt.Printf("pair failed: %v\n", err)
		return
	}
	if pin != "" {
		fmt.Printf("verification code: %s\n", pin)
	}
}

func ExampleClient_PromptRespond() {
	c := client.New("/run/user/1000/kcd/kcd.sock")
	err := c.PromptRespond("prompt-12345", "accept")
	if err != nil {
		fmt.Printf("failed to respond to prompt: %v\n", err)
	}
}

func ExampleClient_OpenKeepAlive() {
	c := client.New("/run/user/1000/kcd/kcd.sock")
	session, err := c.OpenKeepAlive()
	if err != nil {
		fmt.Printf("session open failed: %v\n", err)
		return
	}
	defer session.Close()

	res, err := session.Call("ping", map[string]string{"deviceId": "remote-device-id"}, time.Second)
	if err != nil {
		fmt.Printf("ping failed: %v\n", err)
		return
	}
	_ = res
}

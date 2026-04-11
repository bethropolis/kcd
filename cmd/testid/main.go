package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/bethropolis/kcd/internal/protocol"
)

func main() {
	pkt, err := protocol.NewIdentityPacket(
		"test_device_id_with_underscores",
		"TestDevice",
		"desktop",
		1716,
		[]string{"kdeconnect.ping"},
		[]string{"kdeconnect.ping"},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	data, _ := json.MarshalIndent(pkt, "", "  ")
	fmt.Println(string(data))
}

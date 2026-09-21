package doctor

import (
	"fmt"
	"testing"
)

// Port checks must report the configured tcp_port, not a hardcoded 1716.
func TestPortChecksUseConfiguredPort(t *testing.T) {
	const port = 1816
	udp := checkUDPPort(true, port)
	if udp.Name != fmt.Sprintf("port %d/udp open", port) || !udp.Pass {
		t.Errorf("udp check = %+v, want name port %d/udp open passing", udp, port)
	}
	tcp := checkTCPPort(true, port)
	if tcp.Name != fmt.Sprintf("port %d/tcp open", port) || !tcp.Pass {
		t.Errorf("tcp check = %+v, want name port %d/tcp open passing", tcp, port)
	}
}

package service

import (
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/edge"
)

// Apply that quietly lets nginx fail to bind takes the whole perimeter down, so
// a taken plaintext port has to be refused with the port named.
func TestCheckHTTPPortFreeRefusesATakenPort(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer held.Close()
	port := held.Addr().(*net.TCPAddr).Port

	cfg := edge.Config{HTTPPort: port, HTTPListen: "127.0.0.1"}
	err = checkHTTPPortFree(&edge.Manager{}, cfg)
	if err == nil {
		t.Fatal("a port owned by another process was accepted, so nginx would fail to start")
	}
	if !strings.Contains(err.Error(), strconv.Itoa(port)) {
		t.Fatalf("error %q does not name the port", err)
	}
}

// With the plaintext block disabled there is nothing to check, and demanding a
// free port would break every host that does not serve 80.
func TestCheckHTTPPortFreeIsANoOpWithoutAPort(t *testing.T) {
	if err := checkHTTPPortFree(&edge.Manager{}, edge.Config{HTTPPort: 0}); err != nil {
		t.Fatalf("check ran with the feature off: %v", err)
	}
}

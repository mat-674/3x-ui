package web

import "testing"

// The perimeter is the only public path to the panel. If the panel kept the
// configured address a scanner reaches it directly and skips the decoy's 404.
func TestPanelListenAddressMovesToLoopbackBehindThePerimeter(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		port       int
		want       string
	}{
		{"wildcard", "0.0.0.0", 2053, "127.0.0.1:2053"},
		{"public address", "203.0.113.7", 2053, "127.0.0.1:2053"},
		{"ipv6 wildcard", "::", 2053, "127.0.0.1:2053"},
		{"empty listens everywhere", "", 8443, "127.0.0.1:8443"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := panelListenAddress(tc.configured, tc.port, true); got != tc.want {
				t.Fatalf("listen bound to %q behind the perimeter, want %q", got, tc.want)
			}
		})
	}
}

// Without the perimeter the panel is its own TLS endpoint, so it must keep the
// exact address the operator configured — the pre-perimeter behaviour.
func TestPanelListenAddressKeepsConfiguredAddressWithoutThePerimeter(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		port       int
		want       string
	}{
		{"wildcard", "0.0.0.0", 2053, "0.0.0.0:2053"},
		{"single address", "203.0.113.7", 2053, "203.0.113.7:2053"},
		{"ipv6 loopback", "::1", 2053, "[::1]:2053"},
		{"empty listens everywhere", "", 2053, ":2053"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := panelListenAddress(tc.configured, tc.port, false); got != tc.want {
				t.Fatalf("listen bound to %q without the perimeter, want %q", got, tc.want)
			}
		})
	}
}

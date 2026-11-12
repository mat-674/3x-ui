package service

import (
	"net"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/json_util"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// REALITY hands every non-client TLS handshake to the perimeter. When the perimeter
// is down xray still starts and refuses them all silently — this is the only signal.
func TestEdgeRealityFallbackWarningsNameTheDeadPerimeterFallback(t *testing.T) {
	deadAddr := closedLoopbackAddr(t)
	live, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for a live perimeter: %v", err)
	}
	t.Cleanup(func() { _ = live.Close() })
	_, deadPort, err := net.SplitHostPort(deadAddr)
	if err != nil {
		t.Fatalf("split the reserved address %q: %v", deadAddr, err)
	}

	stream := func(field, dest string) json_util.RawMessage {
		return json_util.RawMessage(`{"network":"tcp","security":"reality","realitySettings":{"` + field + `":"` + dest + `"}}`)
	}
	reality := func(tag, dest string) xray.InboundConfig {
		return xray.InboundConfig{Tag: tag, StreamSettings: stream("dest", dest)}
	}

	cases := []struct {
		name     string
		inbounds []xray.InboundConfig
		edgeAddr string
		want     []string
	}{
		{
			name:     "inbound falls back to a dead perimeter",
			inbounds: []xray.InboundConfig{reality("vless-main", deadAddr)},
			edgeAddr: deadAddr,
			want:     []string{`"vless-main"`, deadAddr},
		},
		{
			name:     "perimeter answers",
			inbounds: []xray.InboundConfig{reality("vless-main", live.Addr().String())},
			edgeAddr: live.Addr().String(),
		},
		{
			name:     "legacy target alias",
			inbounds: []xray.InboundConfig{{Tag: "vless-legacy", StreamSettings: stream("target", deadAddr)}},
			edgeAddr: deadAddr,
			want:     []string{`"vless-legacy"`, deadAddr},
		},
		{
			name:     "fallback to another port",
			inbounds: []xray.InboundConfig{reality("vless-main", "www.example.com:443")},
			edgeAddr: deadAddr,
		},
		{
			name:     "same port on a public host",
			inbounds: []xray.InboundConfig{reality("vless-fronted", "www.example.com:"+deadPort)},
			edgeAddr: deadAddr,
		},
		{
			name:     "inbound without REALITY",
			inbounds: []xray.InboundConfig{{Tag: "trojan", StreamSettings: json_util.RawMessage(`{"network":"tcp","security":"tls"}`)}},
			edgeAddr: deadAddr,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			warnings := edgeRealityFallbackWarnings(tc.inbounds, tc.edgeAddr, dialEdge)
			if len(tc.want) == 0 {
				if len(warnings) != 0 {
					t.Fatalf("warnings = %q, want none", warnings)
				}
				return
			}
			if len(warnings) != 1 {
				t.Fatalf("warnings = %q, want exactly one for %q", warnings, tc.inbounds[0].Tag)
			}
			for _, fragment := range tc.want {
				if !strings.Contains(warnings[0], fragment) {
					t.Fatalf("warning %q does not name %q", warnings[0], fragment)
				}
			}
		})
	}
}

// A wildcard listen is not a connectable address on every platform, so the probe must
// dial loopback — otherwise a healthy perimeter still warns (and on Windows, never stops).
func TestEdgeProbeAddrDialsLoopbackForAWildcardListen(t *testing.T) {
	cases := []struct {
		name   string
		listen string
		port   int
		want   string
	}{
		{"empty listen", "", 8443, "127.0.0.1:8443"},
		{"ipv4 wildcard", "0.0.0.0", 8443, "127.0.0.1:8443"},
		{"ipv6 wildcard", "::", 8443, "127.0.0.1:8443"},
		{"padded wildcard", "  0.0.0.0  ", 8443, "127.0.0.1:8443"},
		{"loopback", "127.0.0.1", 8443, "127.0.0.1:8443"},
		{"specific address", "10.1.2.3", 8443, "10.1.2.3:8443"},
		{"ipv6 loopback", "::1", 8443, "[::1]:8443"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := edgeProbeAddr(tc.listen, tc.port); got != tc.want {
				t.Fatalf("probe address for listen %q = %q, want %q", tc.listen, got, tc.want)
			}
		})
	}
}

// A helper nobody calls looks exactly like a working guard in a unit test, so the
// wiring itself is checked: GetXrayConfig is the only place the warning reaches an
// operator before xray starts refusing every non-client.
func TestGetXrayConfigWarnsOnlyWhenTheRealityFallbackPerimeterIsDown(t *testing.T) {
	setupSettingTestDB(t)
	live, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for a live perimeter: %v", err)
	}
	t.Cleanup(func() { _ = live.Close() })

	setPerimeter := func(t *testing.T, addr string) {
		t.Helper()
		host, portStr, splitErr := net.SplitHostPort(addr)
		if splitErr != nil {
			t.Fatalf("split %q: %v", addr, splitErr)
		}
		s := &SettingService{}
		for key, value := range map[string]string{"edgeEnable": "true", "edgeListen": host, "edgePort": portStr} {
			if saveErr := s.saveSetting(key, value); saveErr != nil {
				t.Fatalf("save %s: %v", key, saveErr)
			}
		}
	}
	seedRealityInbound := func(t *testing.T, tag, dest string, port int) {
		t.Helper()
		inbound := &model.Inbound{
			Tag: tag, Enable: true, Port: port, Protocol: model.VLESS,
			Settings:       `{"clients":[],"decryption":"none"}`,
			StreamSettings: `{"network":"tcp","security":"reality","realitySettings":{"dest":"` + dest + `"}}`,
		}
		if createErr := database.GetDB().Create(inbound).Error; createErr != nil {
			t.Fatalf("seed inbound %q: %v", tag, createErr)
		}
	}

	deadAddr := closedLoopbackAddr(t)
	setPerimeter(t, deadAddr)
	deadTag := "reality-dead-perimeter"
	seedRealityInbound(t, deadTag, deadAddr, 44431)
	if _, cfgErr := (&XrayService{}).GetXrayConfig(); cfgErr != nil {
		t.Fatalf("a dead perimeter must not fail the config, got: %v", cfgErr)
	}
	if !logNamesInbound(t, deadTag) {
		t.Fatalf("no warning named %q; GetXrayConfig did not report the dead REALITY fallback", deadTag)
	}

	setPerimeter(t, live.Addr().String())
	liveTag := "reality-live-perimeter"
	seedRealityInbound(t, liveTag, live.Addr().String(), 44432)
	if _, cfgErr := (&XrayService{}).GetXrayConfig(); cfgErr != nil {
		t.Fatalf("GetXrayConfig with a reachable perimeter: %v", cfgErr)
	}
	if logNamesInbound(t, liveTag) {
		t.Fatalf("a warning named %q even though the perimeter answers on %s", liveTag, live.Addr())
	}
}

// logNamesInbound scans the panel's own log buffer, which is what an operator reads.
func logNamesInbound(t *testing.T, tag string) bool {
	t.Helper()
	for _, entry := range logger.GetLogs(100000, "warning") {
		if strings.Contains(entry, tag) {
			return true
		}
	}
	return false
}

// closedLoopbackAddr reserves a port and releases it, so a dial to the result is
// refused the same way the REALITY fallback would be refused.
func closedLoopbackAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a loopback port: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release the reserved port: %v", err)
	}
	return addr
}

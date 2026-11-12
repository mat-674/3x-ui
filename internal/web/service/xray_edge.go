package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/util/json_util"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// edgeRealityFallbackWarnings names the REALITY inbounds whose fallback target is a
// perimeter that answers nothing; xray would start and then refuse every non-client.
func edgeRealityFallbackWarnings(inbounds []xray.InboundConfig, edgeAddr string, probe func(addr string) error) []string {
	if probe(edgeAddr) == nil {
		return nil
	}
	edgePort := addrPort(edgeAddr)
	if edgePort == 0 {
		return nil
	}
	var warnings []string
	for _, inbound := range inbounds {
		dest, port := realityFallbackDest(inbound.StreamSettings)
		if dest == "" || port != edgePort || !namesInternalHost(dest) {
			continue
		}
		warnings = append(warnings, fmt.Sprintf(
			"Inbound %q: REALITY fallback destination %q is the nginx perimeter and nothing answers on %s yet, so every non-client TLS handshake will be refused",
			inbound.Tag, dest, edgeAddr))
	}
	return warnings
}

// realityFallbackDest returns an inbound's REALITY fallback destination and the port
// it dials; "dest" with "target" as the legacy alias, a bare port allowed.
func realityFallbackDest(stream json_util.RawMessage) (string, int) {
	if len(stream) == 0 {
		return "", 0
	}
	var settings struct {
		Security        string         `json:"security"`
		RealitySettings map[string]any `json:"realitySettings"`
	}
	if err := json.Unmarshal(stream, &settings); err != nil || !strings.EqualFold(settings.Security, "reality") {
		return "", 0
	}
	target, ok := settings.RealitySettings["dest"]
	if !ok {
		target = settings.RealitySettings["target"]
	}
	var text string
	switch v := target.(type) {
	case string:
		text = strings.TrimSpace(v)
	case float64:
		text = strconv.Itoa(int(v))
	}
	return relayTargetPort(text)
}

// relayTargetPort splits a relay target into its text and port; a unix socket or
// anything without a numeric port yields no port, so it cannot match the perimeter.
func relayTargetPort(target string) (string, int) {
	if target == "" || strings.HasPrefix(target, "/") || strings.HasPrefix(target, "@") {
		return "", 0
	}
	if _, portStr, err := net.SplitHostPort(target); err == nil {
		if port, convErr := strconv.Atoi(portStr); convErr == nil {
			return target, port
		}
		return "", 0
	}
	if port, err := strconv.Atoi(target); err == nil {
		return target, port
	}
	return "", 0
}

func addrPort(addr string) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0
	}
	return port
}

// edgeProbeAddr is what a probe dials for the perimeter's listen setting: a wildcard
// bind is not a connectable address on every platform, so it becomes loopback.
func edgeProbeAddr(listen string, port int) string {
	host := strings.TrimSpace(listen)
	if host == "" {
		host = "127.0.0.1"
	} else if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// dialEdge reports whether anything accepts a TCP connection on addr.
func dialEdge(addr string) error {
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(context.Background(), "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}

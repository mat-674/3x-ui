package model

import (
	"encoding/json"
	"fmt"
)

// Balancer is a panel-only pseudo-protocol. A balancer inbound row does not run
// on xray-core; it groups several real inbounds so the JSON subscription can
// emit them as one load-balanced profile (routing.balancers + observatory) for
// the client's own xray. It is skipped everywhere the server config is built.
//
// Visibility is driven entirely by client attachment, exactly like a normal
// inbound: a client assigned to the balancer (client_inbounds + settings.clients)
// gets the balanced profile in its subscription, and is provisioned onto the
// member inbounds' runtime user lists so the member servers accept it.
const Balancer Protocol = "balancer"

const (
	// DefaultBalancerProbeURL / DefaultBalancerProbeInterval seed the
	// observatory the client config needs for the leastPing strategy.
	DefaultBalancerProbeURL      = "https://www.google.com/generate_204"
	DefaultBalancerProbeInterval = "10s"
)

// BalancerSettings is the parsed shape of a balancer inbound's settings JSON,
// stored under the "balancer" key (alongside the usual "clients" array) so the
// column round-trips through the same Inbound.Settings text field every other
// inbound uses.
type BalancerSettings struct {
	// Members are the inbound IDs participating in the balancer.
	Members []int `json:"members"`
	// ProbeURL / ProbeInterval drive the observatory injected into the client
	// JSON config so leastPing has latency samples to rank members by.
	ProbeURL      string `json:"probeUrl"`
	ProbeInterval string `json:"probeInterval"`
}

// ParseBalancerSettings reads the {"balancer":{...}} wrapper out of an inbound's
// settings JSON and normalizes defaults. Errors when the JSON is invalid or the
// balancer key is absent.
func ParseBalancerSettings(settings string) (*BalancerSettings, error) {
	var wrap struct {
		Balancer *BalancerSettings `json:"balancer"`
	}
	if err := json.Unmarshal([]byte(settings), &wrap); err != nil {
		return nil, err
	}
	if wrap.Balancer == nil {
		return nil, fmt.Errorf("balancer settings missing")
	}
	wrap.Balancer.normalize()
	return wrap.Balancer, nil
}

func (b *BalancerSettings) normalize() {
	if b.ProbeURL == "" {
		b.ProbeURL = DefaultBalancerProbeURL
	}
	if b.ProbeInterval == "" {
		b.ProbeInterval = DefaultBalancerProbeInterval
	}
	seen := make(map[int]struct{}, len(b.Members))
	out := make([]int, 0, len(b.Members))
	for _, m := range b.Members {
		if m <= 0 {
			continue
		}
		if _, dup := seen[m]; dup {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}
	b.Members = out
}

// JSON re-serializes the balancer settings into the {"balancer":{...}} wrapper
// the settings column stores (clients are merged in separately by the service).
func (b *BalancerSettings) JSON() string {
	raw, _ := json.MarshalIndent(map[string]any{"balancer": b}, "", "  ")
	return string(raw)
}

// Contains reports whether the given inbound id is one of the balancer's members.
func (b *BalancerSettings) Contains(inboundId int) bool {
	for _, m := range b.Members {
		if m == inboundId {
			return true
		}
	}
	return false
}

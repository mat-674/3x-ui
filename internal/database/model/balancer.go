package model

import (
	"encoding/json"
	"fmt"
)

// Balancer is a panel-only pseudo-protocol. A balancer inbound row does not run
// on xray-core; it groups several real inbounds so the JSON subscription can
// emit them as one load-balanced profile (routing.balancers + observatory) for
// the client's own xray. It is skipped everywhere the server config is built.
const Balancer Protocol = "balancer"

const (
	// DefaultBalancerProbeURL / DefaultBalancerProbeInterval seed the
	// observatory the client config needs for the leastPing strategy.
	DefaultBalancerProbeURL      = "https://www.google.com/generate_204"
	DefaultBalancerProbeInterval = "10s"

	// BalancerVisibilityAll exposes the balancer to every subscription that
	// has a client on at least two member inbounds (the default). Anything
	// else is treated as BalancerVisibilitySelected.
	BalancerVisibilityAll      = "all"
	BalancerVisibilitySelected = "selected"
)

// BalancerSettings is the parsed shape of a balancer inbound's settings JSON,
// stored under the "balancer" key so the column round-trips through the same
// Inbound.Settings text field every other inbound uses.
type BalancerSettings struct {
	// Members are the inbound IDs participating in the balancer.
	Members []int `json:"members"`
	// ProbeURL / ProbeInterval drive the observatory injected into the client
	// JSON config so leastPing has latency samples to rank members by.
	ProbeURL      string `json:"probeUrl"`
	ProbeInterval string `json:"probeInterval"`
	// Visibility selects who sees the balancer in their JSON subscription:
	//   - "all" (default): every subscription with a client on >=2 members;
	//   - "selected": only the subscription IDs listed in SubIds.
	Visibility string `json:"visibility"`
	// SubIds is the explicit allow-list of subscription IDs the balancer is
	// shown to when Visibility == "selected". Empty list + "selected" hides the
	// balancer from everyone.
	SubIds []string `json:"subIds"`
}

// VisibleTo reports whether the balancer should be emitted for the given
// subscription ID. "all" always shows; "selected" shows only when subId is in
// the allow-list.
func (b *BalancerSettings) VisibleTo(subId string) bool {
	if b.Visibility != BalancerVisibilitySelected {
		return true
	}
	for _, id := range b.SubIds {
		if id == subId {
			return true
		}
	}
	return false
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

	if b.Visibility != BalancerVisibilitySelected {
		b.Visibility = BalancerVisibilityAll
	}
	seenSub := make(map[string]struct{}, len(b.SubIds))
	subIds := make([]string, 0, len(b.SubIds))
	for _, id := range b.SubIds {
		if id == "" {
			continue
		}
		if _, dup := seenSub[id]; dup {
			continue
		}
		seenSub[id] = struct{}{}
		subIds = append(subIds, id)
	}
	b.SubIds = subIds
}

// JSON re-serializes the balancer settings back into the {"balancer":{...}}
// wrapper the settings column stores.
func (b *BalancerSettings) JSON() string {
	raw, _ := json.MarshalIndent(map[string]any{"balancer": b}, "", "  ")
	return string(raw)
}

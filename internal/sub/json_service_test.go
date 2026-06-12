package sub

import (
	"encoding/json"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func hasDirectOutOutbound(svc *SubJsonService) bool {
	for _, raw := range svc.defaultOutbounds {
		var outbound map[string]any
		if err := json.Unmarshal(raw, &outbound); err != nil {
			continue
		}
		if outbound["tag"] == "direct_out" {
			return true
		}
	}
	return false
}

func outboundSettings(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("failed to unmarshal outbound: %v", err)
	}
	settings, _ := parsed["settings"].(map[string]any)
	if settings == nil {
		t.Fatal("outbound has no settings")
	}
	return settings
}

func TestSubJsonServiceInjectsGlobalFinalMask(t *testing.T) {
	finalMask := `{"tcp":[{"type":"fragment","settings":{"packets":"tlshello","length":"100-200","delay":"10-20"}}],"udp":[{"type":"noise","settings":{"noise":[{"type":"base64","packet":"SGVsbG8="}]}}],"quicParams":{"congestion":"bbr"}}`
	svc := NewSubJsonService("", "", finalMask, nil)

	if hasDirectOutOutbound(svc) {
		t.Fatal("direct_out outbound must never be emitted")
	}

	stream := svc.streamData(`{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}}}`)
	if _, ok := stream["sockopt"]; ok {
		t.Fatal("legacy direct_out dialerProxy sockopt must never be set")
	}

	finalmask, _ := stream["finalmask"].(map[string]any)
	if finalmask == nil {
		t.Fatal("streamSettings is missing finalmask")
	}

	tcp, _ := finalmask["tcp"].([]any)
	if len(tcp) != 1 {
		t.Fatalf("tcp masks len = %d, want 1", len(tcp))
	}
	if first, _ := tcp[0].(map[string]any); first["type"] != "fragment" {
		t.Fatalf("tcp[0] type = %v, want fragment", first["type"])
	}

	udp, _ := finalmask["udp"].([]any)
	if len(udp) != 1 {
		t.Fatalf("udp masks len = %d, want 1", len(udp))
	}

	quic, _ := finalmask["quicParams"].(map[string]any)
	if quic == nil || quic["congestion"] != "bbr" {
		t.Fatalf("quicParams missing/wrong: %#v", finalmask["quicParams"])
	}
}

func TestSubJsonServiceMergesWithExistingFinalMask(t *testing.T) {
	finalMask := `{"tcp":[{"type":"fragment","settings":{"packets":"tlshello"}}]}`
	svc := NewSubJsonService("", "", finalMask, nil)

	stream := svc.streamData(`{
		"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}},
		"finalmask":{"tcp":[{"type":"sudoku"}]}
	}`)

	finalmask, _ := stream["finalmask"].(map[string]any)
	tcp, _ := finalmask["tcp"].([]any)
	if len(tcp) != 2 {
		t.Fatalf("tcp masks len = %d, want 2 (existing + global)", len(tcp))
	}
	a, _ := tcp[0].(map[string]any)
	b, _ := tcp[1].(map[string]any)
	if a["type"] != "sudoku" || b["type"] != "fragment" {
		t.Fatalf("tcp masks = %#v, want existing sudoku then global fragment", tcp)
	}
}

func TestSubJsonServiceNoFinalMaskWhenEmpty(t *testing.T) {
	svc := NewSubJsonService("", "", "", nil)
	stream := svc.streamData(`{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}}}`)
	if _, ok := stream["finalmask"]; ok {
		t.Fatal("no finalmask should be emitted when subJsonFinalMask is empty")
	}
	if _, ok := stream["sockopt"]; ok {
		t.Fatal("legacy direct_out sockopt must never be set")
	}
}

func TestSubJsonServiceVlessFlattened(t *testing.T) {
	inbound := &model.Inbound{Listen: "1.2.3.4", Port: 443, Protocol: model.VLESS, Settings: `{"encryption":"none"}`}
	client := model.Client{ID: "uuid-1", Flow: "xtls-rprx-vision"}

	settings := outboundSettings(t, NewSubJsonService("", "", "", nil).genVless(inbound, nil, client))
	if _, ok := settings["vnext"]; ok {
		t.Fatal("vless outbound must not use vnext")
	}
	if settings["address"] != "1.2.3.4" || settings["id"] != "uuid-1" || settings["encryption"] != "none" || settings["flow"] != "xtls-rprx-vision" {
		t.Fatalf("flat vless settings wrong: %#v", settings)
	}
}

func TestSubJsonServiceVmessFlattened(t *testing.T) {
	inbound := &model.Inbound{Listen: "1.2.3.4", Port: 443, Protocol: model.VMESS, Settings: `{}`}
	client := model.Client{ID: "uuid-2"}

	settings := outboundSettings(t, NewSubJsonService("", "", "", nil).genVnext(inbound, nil, client))
	if _, ok := settings["vnext"]; ok {
		t.Fatal("vmess outbound must not use vnext")
	}
	if settings["id"] != "uuid-2" || settings["security"] != "auto" {
		t.Fatalf("flat vmess settings wrong: %#v", settings)
	}
}

func TestSubJsonServiceBalancerConfig(t *testing.T) {
	sub := NewSubService(false, "-io")
	svc := NewSubJsonService("", "", "", sub)

	balancer := &model.Inbound{
		Id:       10,
		Protocol: model.Balancer,
		Remark:   "EU-LB",
		Enable:   true,
		Settings: (&model.BalancerSettings{Members: []int{1, 2}}).JSON(),
	}
	mkMember := func(id int, proto model.Protocol) *model.Inbound {
		return &model.Inbound{
			Id:             id,
			Protocol:       proto,
			Enable:         true,
			Listen:         "1.2.3.4",
			Port:           443,
			StreamSettings: `{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}}}`,
			Settings:       `{"encryption":"none","clients":[{"id":"uuid-` + string(rune('0'+id)) + `","email":"e` + string(rune('0'+id)) + `","subId":"sub1"}]}`,
		}
	}
	members := []*model.Inbound{mkMember(1, model.VLESS), mkMember(2, model.VMESS)}

	raw, ok := svc.getBalancerConfig(balancer, members, "sub1", "fallback.host")
	if !ok {
		t.Fatal("expected balancer config to be produced")
	}

	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("balancer config is not valid JSON: %v", err)
	}

	// Two proxy-N outbounds plus the default direct/block outbounds.
	outbounds, _ := cfg["outbounds"].([]any)
	tags := map[string]bool{}
	for _, o := range outbounds {
		ob, _ := o.(map[string]any)
		if tag, _ := ob["tag"].(string); tag != "" {
			tags[tag] = true
		}
	}
	if !tags["proxy-1"] || !tags["proxy-2"] {
		t.Fatalf("expected proxy-1 and proxy-2 outbounds, got tags %v", tags)
	}
	if tags["proxy"] {
		t.Fatal("balancer outbounds must be retagged away from plain 'proxy'")
	}

	routing, _ := cfg["routing"].(map[string]any)
	balancers, _ := routing["balancers"].([]any)
	if len(balancers) != 1 {
		t.Fatalf("expected exactly one balancer, got %d", len(balancers))
	}
	b0, _ := balancers[0].(map[string]any)
	if b0["tag"] != "balancer" {
		t.Fatalf("balancer tag = %v, want 'balancer'", b0["tag"])
	}
	strategy, _ := b0["strategy"].(map[string]any)
	if strategy["type"] != "leastPing" {
		t.Fatalf("balancer strategy = %v, want leastPing", strategy["type"])
	}

	rules, _ := routing["rules"].([]any)
	foundBalancerRule := false
	for _, r := range rules {
		rule, _ := r.(map[string]any)
		if rule["balancerTag"] == "balancer" {
			foundBalancerRule = true
			if _, hasOut := rule["outboundTag"]; hasOut {
				t.Fatal("rule must not keep outboundTag once balancerTag is set")
			}
		}
	}
	if !foundBalancerRule {
		t.Fatal("expected a routing rule pointing at the balancer")
	}

	obs, _ := cfg["observatory"].(map[string]any)
	if obs == nil {
		t.Fatal("expected an observatory for leastPing probing")
	}
	if obs["probeURL"] != model.DefaultBalancerProbeURL {
		t.Fatalf("observatory probeURL = %v, want default", obs["probeURL"])
	}
}

func TestSubJsonServiceBalancerVisibilitySelected(t *testing.T) {
	sub := NewSubService(false, "-io")
	svc := NewSubJsonService("", "", "", sub)

	mkMember := func(id int, proto model.Protocol, email string) *model.Inbound {
		return &model.Inbound{
			Id: id, Protocol: proto, Enable: true, Listen: "1.2.3.4", Port: 443,
			StreamSettings: `{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}}}`,
			Settings:       `{"encryption":"none","clients":[{"id":"uuid-` + email + `","email":"` + email + `","subId":"sub1"}]}`,
		}
	}
	members := []*model.Inbound{mkMember(1, model.VLESS, "alice"), mkMember(2, model.VMESS, "alice")}

	// Selected, allow-list = [sub2] → sub1 must NOT see it.
	hidden := &model.Inbound{Id: 10, Protocol: model.Balancer, Enable: true,
		Settings: (&model.BalancerSettings{Members: []int{1, 2}, Visibility: model.BalancerVisibilitySelected, SubIds: []string{"sub2"}}).JSON()}
	if _, ok := svc.getBalancerConfig(hidden, members, "sub1", "h"); ok {
		t.Fatal("selected balancer must be hidden from a subId not in its allow-list")
	}

	// Selected, allow-list = [sub1] → sub1 sees it.
	shown := &model.Inbound{Id: 10, Protocol: model.Balancer, Enable: true,
		Settings: (&model.BalancerSettings{Members: []int{1, 2}, Visibility: model.BalancerVisibilitySelected, SubIds: []string{"sub1"}}).JSON()}
	if _, ok := svc.getBalancerConfig(shown, members, "sub1", "h"); !ok {
		t.Fatal("selected balancer must be visible to an allow-listed subId")
	}
}

func TestSubJsonServiceBalancerNeedsTwoEndpoints(t *testing.T) {
	sub := NewSubService(false, "-io")
	svc := NewSubJsonService("", "", "", sub)

	balancer := &model.Inbound{Id: 10, Protocol: model.Balancer, Enable: true, Settings: (&model.BalancerSettings{Members: []int{1}}).JSON()}
	member := &model.Inbound{
		Id: 1, Protocol: model.VLESS, Enable: true, Listen: "1.2.3.4", Port: 443,
		StreamSettings: `{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}}}`,
		Settings:       `{"encryption":"none","clients":[{"id":"uuid-1","email":"e1","subId":"sub1"}]}`,
	}
	if _, ok := svc.getBalancerConfig(balancer, []*model.Inbound{member}, "sub1", "h"); ok {
		t.Fatal("a single endpoint must not yield a balancer config")
	}
}

func TestSubJsonServiceServerFlattened(t *testing.T) {
	trojan := &model.Inbound{Listen: "1.2.3.4", Port: 443, Protocol: model.Trojan, Settings: `{}`}
	client := model.Client{Password: "p4ss"}

	settings := outboundSettings(t, NewSubJsonService("", "", "", nil).genServer(trojan, nil, client))
	if _, ok := settings["servers"]; ok {
		t.Fatal("trojan outbound must not use servers array")
	}
	if settings["password"] != "p4ss" || settings["address"] != "1.2.3.4" {
		t.Fatalf("flat trojan settings wrong: %#v", settings)
	}

	ss := &model.Inbound{Listen: "1.2.3.4", Port: 443, Protocol: model.Shadowsocks, Settings: `{"method":"aes-256-gcm"}`}
	ssSettings := outboundSettings(t, NewSubJsonService("", "", "", nil).genServer(ss, nil, client))
	if ssSettings["method"] != "aes-256-gcm" {
		t.Fatalf("flat shadowsocks must carry method: %#v", ssSettings)
	}
}

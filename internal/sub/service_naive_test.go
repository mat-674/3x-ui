package sub

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func naiveSubscriptionInbound(streamSettings string) *model.Inbound {
	return &model.Inbound{
		Listen:         "0.0.0.0",
		Port:           443,
		Protocol:       model.Naive,
		Remark:         "naive-share",
		Enable:         true,
		Settings:       `{"domain":"naive.example.com","clients":[{"email":"alice@example.com","password":"p@ss","enable":true,"subId":"naive-sub"}]}`,
		StreamSettings: streamSettings,
	}
}

func TestGenNaiveLinkUsesDomainAndExternalEndpoint(t *testing.T) {
	inbound := naiveSubscriptionInbound(`{"security":"tls","tlsSettings":{"certificates":[{"certificateFile":"/cert.pem","keyFile":"/key.pem"}]}}`)
	s := &SubService{address: "request.example.com"}

	link := s.genNaiveLink(inbound, "alice@example.com")
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse default link: %v", err)
	}
	if u.Scheme != "naive+https" || u.Host != "naive.example.com:443" {
		t.Fatalf("default link = %q, want naive+https://...@naive.example.com:443", link)
	}
	if u.User.Username() != "alice@example.com" {
		t.Fatalf("username = %q", u.User.Username())
	}
	password, ok := u.User.Password()
	if !ok || password != "p@ss" {
		t.Fatalf("password = %q, present=%v", password, ok)
	}

	inbound.StreamSettings = `{"security":"tls","externalProxy":[{"dest":"edge.example.com","port":9443,"remark":"edge","sni":"origin.example.com","allowInsecure":true}]}`
	links := strings.Split(s.genNaiveLink(inbound, "alice@example.com"), "\n")
	if len(links) != 1 {
		t.Fatalf("external links = %d, want 1: %v", len(links), links)
	}
	external, err := url.Parse(links[0])
	if err != nil {
		t.Fatalf("parse external link: %v", err)
	}
	if external.Host != "edge.example.com:9443" {
		t.Fatalf("external host = %q", external.Host)
	}
	if external.Query().Get("sni") != "origin.example.com" || external.Query().Get("allow_insecure") != "1" {
		t.Fatalf("external query = %q, want sni and allow_insecure", external.RawQuery)
	}
	if !strings.Contains(external.Fragment, "edge") {
		t.Fatalf("external fragment = %q, want edge", external.Fragment)
	}
}

func TestNaiveJSONUsesNativeClientShape(t *testing.T) {
	inbound := naiveSubscriptionInbound(`{"security":"tls"}`)
	subReq := &SubService{address: "request.example.com"}
	jsonService := &SubJsonService{}
	inbound.StreamSettings = `{"security":"tls","externalProxy":[{"dest":"edge.example.com","port":9443,"sni":"origin.example.com","allowInsecure":true}]}`
	configs := jsonService.getConfig(subReq, inbound, model.Client{
		Email:    "alice@example.com",
		Password: "p@ss",
		Enable:   true,
	}, "request.example.com")
	if len(configs) != 1 {
		t.Fatalf("configs = %d, want 1", len(configs))
	}

	var got map[string]any
	if err := json.Unmarshal(configs[0], &got); err != nil {
		t.Fatalf("unmarshal native config: %v", err)
	}
	if got["listen"] != "socks://127.0.0.1:1080" {
		t.Fatalf("listen = %v", got["listen"])
	}
	if got["proxy"] != "https://alice%40example.com:p%40ss@edge.example.com:9443?allow_insecure=1&sni=origin.example.com" {
		t.Fatalf("proxy = %v", got["proxy"])
	}
	if _, exists := got["outbounds"]; exists {
		t.Fatalf("native Naive config must not contain Xray outbounds: %#v", got)
	}
}

func TestBuildNaiveProxyUsesTLSHTTPConnect(t *testing.T) {
	subReq := &SubService{address: "request.example.com"}
	svc := &SubClashService{SubService: subReq}
	inbound := naiveSubscriptionInbound(`{"security":"tls","externalProxy":[{"dest":"edge.example.com","port":9443,"sni":"naive.example.com","allowInsecure":true}]}`)
	proxies := svc.getProxies(subReq, inbound, model.Client{
		Email:    "alice@example.com",
		Password: "p@ss",
	}, "request.example.com")
	if len(proxies) != 1 {
		t.Fatalf("proxies = %d, want 1: %#v", len(proxies), proxies)
	}
	proxy := proxies[0]
	for key, want := range map[string]any{
		"type":             "http",
		"server":           "edge.example.com",
		"port":             9443,
		"username":         "alice@example.com",
		"password":         "p@ss",
		"tls":              true,
		"servername":       "naive.example.com",
		"skip-cert-verify": true,
	} {
		if proxy[key] != want {
			t.Fatalf("proxy[%q] = %v, want %v; proxy=%#v", key, proxy[key], want, proxy)
		}
	}
}

func TestGetInboundsBySubIdIncludesNaive(t *testing.T) {
	initSubDB(t)
	db := database.GetDB()
	inbound := naiveSubscriptionInbound(`{"security":"tls"}`)
	if err := db.Create(inbound).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	client := &model.ClientRecord{Email: "alice@example.com", SubID: "naive-sub", Enable: true, Password: "p@ss"}
	if err := db.Create(client).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := db.Create(&model.ClientInbound{ClientId: client.Id, InboundId: inbound.Id}).Error; err != nil {
		t.Fatalf("attach client: %v", err)
	}

	inbounds, err := (&SubService{}).getInboundsBySubId("naive-sub")
	if err != nil {
		t.Fatalf("getInboundsBySubId: %v", err)
	}
	if len(inbounds) != 1 || inbounds[0].Id != inbound.Id {
		t.Fatalf("Naive inbound not returned: %+v", inbounds)
	}
}

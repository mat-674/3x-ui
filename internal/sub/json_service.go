package sub

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/json_util"
	"github.com/mhsanaei/3x-ui/v3/internal/util/random"
)

//go:embed default.json
var defaultJson string

// SubJsonService handles JSON subscription configuration generation and management.
type SubJsonService struct {
	configJson       map[string]any
	defaultOutbounds []json_util.RawMessage
	finalMask        string
	mux              string

	SubService *SubService
}

// NewSubJsonService creates a new JSON subscription service with the given configuration.
func NewSubJsonService(mux string, rules string, finalMask string, subService *SubService) *SubJsonService {
	var configJson map[string]any
	var defaultOutbounds []json_util.RawMessage
	json.Unmarshal([]byte(defaultJson), &configJson)
	if outboundSlices, ok := configJson["outbounds"].([]any); ok {
		for _, defaultOutbound := range outboundSlices {
			jsonBytes, _ := json.Marshal(defaultOutbound)
			defaultOutbounds = append(defaultOutbounds, jsonBytes)
		}
	}

	if rules != "" {
		var newRules []any
		routing, _ := configJson["routing"].(map[string]any)
		defaultRules, _ := routing["rules"].([]any)
		json.Unmarshal([]byte(rules), &newRules)
		defaultRules = append(newRules, defaultRules...)
		routing["rules"] = defaultRules
		configJson["routing"] = routing
	}

	return &SubJsonService{
		configJson:       configJson,
		defaultOutbounds: defaultOutbounds,
		finalMask:        finalMask,
		mux:              mux,
		SubService:       subService,
	}
}

// GetJson generates a JSON subscription configuration for the given subscription ID and host.
func (s *SubJsonService) GetJson(subId string, host string) (string, string, error) {
	// Set per-request state on the shared SubService so any
	// resolveInboundAddress call inside picks node-aware host values.
	s.SubService.PrepareForRequest(host)
	inbounds, err := s.SubService.getInboundsBySubId(subId)
	if err != nil {
		return "", "", err
	}
	// Note: an empty inbounds list is not a short-circuit — a client assigned
	// only to a balancer has no regular inbounds yet still gets the balanced
	// config below. The final len(configArray) check handles "truly empty".

	var header string
	var configArray []json_util.RawMessage

	seenEmails := make(map[string]struct{})
	// Prepare Inbounds
	for _, inbound := range inbounds {
		clients := s.SubService.matchingClients(inbound, subId)
		if len(clients) == 0 {
			continue
		}
		s.SubService.projectThroughFallbackMaster(inbound)

		for _, client := range clients {
			seenEmails[client.Email] = struct{}{}
			configArray = append(configArray, s.getConfig(inbound, client, host)...)
		}
	}

	// Append one balanced config per enabled balancer. A balancer groups
	// several real inbounds into a single leastPing-balanced profile that
	// shows up alongside the regular per-node entries.
	for _, balancer := range s.SubService.getEnabledBalancers() {
		settings, err := model.ParseBalancerSettings(balancer.Settings)
		if err != nil || len(settings.Members) == 0 {
			continue
		}
		members := s.SubService.getInboundsByIds(settings.Members)
		if raw, ok := s.getBalancerConfig(balancer, members, subId, host); ok {
			configArray = append(configArray, raw)
		}
	}

	if len(configArray) == 0 {
		return "", "", nil
	}

	emails := make([]string, 0, len(seenEmails))
	for e := range seenEmails {
		emails = append(emails, e)
	}
	traffic, _ := s.SubService.AggregateTrafficByEmails(emails)

	// Combile outbounds
	var finalJson []byte
	if len(configArray) == 1 {
		finalJson, _ = json.MarshalIndent(configArray[0], "", "  ")
	} else {
		finalJson, _ = json.MarshalIndent(configArray, "", "  ")
	}

	header = fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d", traffic.Up, traffic.Down, traffic.Total, traffic.ExpiryTime/1000)
	return string(finalJson), header, nil
}

// outboundVariant is one proxy outbound (tag "proxy") plus the remark to use
// for the config that wraps it. An inbound with several External Proxy entries
// yields several variants.
type outboundVariant struct {
	raw    json_util.RawMessage
	remark string
}

// buildOutboundVariants turns one (inbound, client) pair into the proxy
// outbound(s) it produces — one per External Proxy entry, or a single synthetic
// one when none is configured. Shared by the per-inbound config builder and the
// balancer builder so both honour the same stream/TLS/external-proxy handling.
func (s *SubJsonService) buildOutboundVariants(inbound *model.Inbound, client model.Client, host string) []outboundVariant {
	stream := s.streamData(inbound.StreamSettings)

	// When externalProxy is empty the JSON config falls back to a
	// synthetic one whose `dest` is the host the client connects to.
	// For node-managed inbounds we want the node's address — request
	// host won't reach the right xray. resolveInboundAddress already
	// implements the node→subscriber-host fallback chain.
	defaultDest := s.SubService.resolveInboundAddress(inbound)
	if defaultDest == "" {
		defaultDest = host
	}

	externalProxies, ok := stream["externalProxy"].([]any)
	hasExternalProxy := ok && len(externalProxies) > 0
	if !hasExternalProxy {
		externalProxies = []any{
			map[string]any{
				"forceTls": "same",
				"dest":     defaultDest,
				"port":     float64(inbound.Port),
				"remark":   "",
			},
		}
	}

	delete(stream, "externalProxy")

	var variants []outboundVariant
	for _, ep := range externalProxies {
		extPrxy := ep.(map[string]any)
		inbound.Listen = extPrxy["dest"].(string)
		inbound.Port = int(extPrxy["port"].(float64))
		newStream := cloneStreamForExternalProxy(stream)
		switch extPrxy["forceTls"].(string) {
		case "tls":
			if newStream["security"] != "tls" {
				newStream["security"] = "tls"
				newStream["tlsSettings"] = map[string]any{}
			}
		case "none":
			if newStream["security"] != "none" {
				newStream["security"] = "none"
				delete(newStream, "tlsSettings")
			}
		}
		security, _ := newStream["security"].(string)
		if hasExternalProxy {
			applyExternalProxyTLSToStream(extPrxy, newStream, security)
		}
		streamSettings, _ := json.MarshalIndent(newStream, "", "  ")

		var raw json_util.RawMessage
		switch inbound.Protocol {
		case "vmess":
			raw = s.genVnext(inbound, streamSettings, client)
		case "vless":
			raw = s.genVless(inbound, streamSettings, client)
		case "trojan", "shadowsocks":
			raw = s.genServer(inbound, streamSettings, client)
		case "hysteria":
			raw = s.genHy(inbound, newStream, client)
		default:
			continue
		}

		variants = append(variants, outboundVariant{
			raw:    raw,
			remark: s.SubService.genRemark(inbound, client.Email, extPrxy["remark"].(string)),
		})
	}
	return variants
}

func (s *SubJsonService) getConfig(inbound *model.Inbound, client model.Client, host string) []json_util.RawMessage {
	var newJsonArray []json_util.RawMessage
	for _, variant := range s.buildOutboundVariants(inbound, client, host) {
		newOutbounds := append([]json_util.RawMessage{variant.raw}, s.defaultOutbounds...)
		newConfigJson := make(map[string]any)
		maps.Copy(newConfigJson, s.configJson)

		newConfigJson["outbounds"] = newOutbounds
		newConfigJson["remarks"] = variant.remark

		newConfig, _ := json.MarshalIndent(newConfigJson, "", "  ")
		newJsonArray = append(newJsonArray, newConfig)
	}

	return newJsonArray
}

// getBalancerConfig emits one JSON config that load-balances across the
// balancer's member inbounds for a client assigned to the balancer. Visibility
// is driven purely by client assignment: the balancer is shown only when this
// subId owns a client on the balancer itself. Each member contributes one or
// more proxy-N outbounds built from that client's credentials; an xray balancer
// with the leastPing strategy plus an observatory selects between them on the
// client. Returns false when fewer than two outbounds materialize.
func (s *SubJsonService) getBalancerConfig(balancer *model.Inbound, members []*model.Inbound, subId string, host string) (json_util.RawMessage, bool) {
	settings, err := model.ParseBalancerSettings(balancer.Settings)
	if err != nil {
		return nil, false
	}

	// The clients assigned to the balancer (same attach flow as any inbound)
	// are the sole visibility gate — no client for this subId means the
	// balancer is not part of this subscription.
	clients := s.SubService.matchingClients(balancer, subId)
	if len(clients) == 0 {
		return nil, false
	}

	var outbounds []json_util.RawMessage
	var firstEmail string
	idx := 0
	for _, member := range members {
		if member == nil || !member.Enable || member.Protocol == model.Balancer {
			continue
		}
		s.SubService.projectThroughFallbackMaster(member)
		for _, client := range clients {
			// Copy the member per client so buildOutboundVariants' in-place
			// Listen/Port mutation doesn't bleed across iterations.
			m := *member
			for _, variant := range s.buildOutboundVariants(&m, client, host) {
				idx++
				tag := fmt.Sprintf("proxy-%d", idx)
				outbounds = append(outbounds, retagOutbound(variant.raw, tag))
				if firstEmail == "" {
					firstEmail = client.Email
				}
			}
		}
	}

	if len(outbounds) < 2 {
		return nil, false
	}

	cfg := deepCopyConfigMap(s.configJson)
	cfg["outbounds"] = append(outbounds, s.defaultOutbounds...)
	remark := s.SubService.genRemark(balancer, firstEmail, "")
	if remark == "" {
		remark = balancer.Tag
	}
	cfg["remarks"] = remark

	const balancerTag = "balancer"
	if routing, ok := cfg["routing"].(map[string]any); ok {
		routing["balancers"] = []any{
			map[string]any{
				"tag":      balancerTag,
				"selector": []string{"proxy-"},
				"strategy": map[string]any{"type": "leastPing"},
			},
		}
		// Point any rule that targeted the single "proxy" outbound at the
		// balancer instead, so all matched traffic is load-balanced.
		if rules, ok := routing["rules"].([]any); ok {
			for _, r := range rules {
				rule, ok := r.(map[string]any)
				if !ok {
					continue
				}
				if rule["outboundTag"] == "proxy" {
					delete(rule, "outboundTag")
					rule["balancerTag"] = balancerTag
				}
			}
		}
	}
	cfg["observatory"] = map[string]any{
		"subjectSelector": []string{"proxy-"},
		"probeURL":        settings.ProbeURL,
		"probeInterval":   settings.ProbeInterval,
	}

	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, false
	}
	return raw, true
}

// retagOutbound rewrites an outbound's "tag" field. The protocol generators all
// emit tag "proxy"; the balancer needs each member endpoint under a distinct
// proxy-N tag so the selector can rank them.
func retagOutbound(raw json_util.RawMessage, tag string) json_util.RawMessage {
	var ob map[string]any
	if err := json.Unmarshal(raw, &ob); err != nil {
		return raw
	}
	ob["tag"] = tag
	out, err := json.MarshalIndent(ob, "", "  ")
	if err != nil {
		return raw
	}
	return out
}

// deepCopyConfigMap clones the parsed template config so per-balancer mutations
// (routing.balancers, observatory, rewritten rules) don't bleed into the shared
// service template or other configs in the same response.
func deepCopyConfigMap(m map[string]any) map[string]any {
	b, err := json.Marshal(m)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func (s *SubJsonService) streamData(stream string) map[string]any {
	var streamSettings map[string]any
	json.Unmarshal([]byte(stream), &streamSettings)
	security, _ := streamSettings["security"].(string)
	switch security {
	case "tls":
		streamSettings["tlsSettings"] = s.tlsData(streamSettings["tlsSettings"].(map[string]any))
	case "reality":
		streamSettings["realitySettings"] = s.realityData(streamSettings["realitySettings"].(map[string]any))
	}
	delete(streamSettings, "sockopt")

	if s.finalMask != "" {
		s.applyGlobalFinalMask(streamSettings)
	}

	// remove proxy protocol
	network, _ := streamSettings["network"].(string)
	switch network {
	case "tcp":
		streamSettings["tcpSettings"] = s.removeAcceptProxy(streamSettings["tcpSettings"])
	case "ws":
		streamSettings["wsSettings"] = s.removeAcceptProxy(streamSettings["wsSettings"])
	case "httpupgrade":
		streamSettings["httpupgradeSettings"] = s.removeAcceptProxy(streamSettings["httpupgradeSettings"])
	case "xhttp":
		streamSettings["xhttpSettings"] = s.removeAcceptProxy(streamSettings["xhttpSettings"])
		if xhttp, ok := streamSettings["xhttpSettings"].(map[string]any); ok {
			delete(xhttp, "noSSEHeader")
			delete(xhttp, "scMaxBufferedPosts")
			delete(xhttp, "scStreamUpServerSecs")
			delete(xhttp, "serverMaxHeaderBytes")
			// Values matching xray-core's own defaults stay off the wire:
			// old panels seeded them into every stored config and the
			// literal scMinPostsIntervalMs=30 is a DPI fingerprint (#5141).
			if v, _ := xhttp["scMaxEachPostBytes"].(string); v == "" || v == "1000000" {
				delete(xhttp, "scMaxEachPostBytes")
			}
			if v, _ := xhttp["scMinPostsIntervalMs"].(string); v == "" || v == "30" {
				delete(xhttp, "scMinPostsIntervalMs")
			}
		}
	}
	return streamSettings
}

func (s *SubJsonService) applyGlobalFinalMask(streamSettings map[string]any) {
	var fm map[string]any
	if err := json.Unmarshal([]byte(s.finalMask), &fm); err != nil || len(fm) == 0 {
		return
	}
	merged := mergeFinalMask(streamSettings["finalmask"], fm)
	if len(merged) > 0 {
		streamSettings["finalmask"] = merged
	}
}

func (s *SubJsonService) removeAcceptProxy(setting any) map[string]any {
	netSettings, ok := setting.(map[string]any)
	if ok {
		delete(netSettings, "acceptProxyProtocol")
	}
	return netSettings
}

func (s *SubJsonService) tlsData(tData map[string]any) map[string]any {
	tlsData := make(map[string]any, 1)
	tlsClientSettings, _ := tData["settings"].(map[string]any)

	tlsData["serverName"] = tData["serverName"]
	tlsData["alpn"] = tData["alpn"]
	if fingerprint, ok := tlsClientSettings["fingerprint"].(string); ok {
		tlsData["fingerprint"] = fingerprint
	}
	if ech, ok := tlsClientSettings["echConfigList"].(string); ok && ech != "" {
		tlsData["echConfigList"] = ech
	}
	if pins, ok := tlsClientSettings["pinnedPeerCertSha256"].([]any); ok && len(pins) > 0 {
		tlsData["pinnedPeerCertSha256"] = pins
	}
	return tlsData
}

func (s *SubJsonService) realityData(rData map[string]any) map[string]any {
	rltyData := make(map[string]any, 1)
	rltyClientSettings, _ := rData["settings"].(map[string]any)

	rltyData["show"] = false
	rltyData["publicKey"] = rltyClientSettings["publicKey"]
	rltyData["fingerprint"] = rltyClientSettings["fingerprint"]
	rltyData["mldsa65Verify"] = rltyClientSettings["mldsa65Verify"]

	// Set random data
	rltyData["spiderX"] = "/" + random.Seq(15)
	shortIds, ok := rData["shortIds"].([]any)
	if ok && len(shortIds) > 0 {
		rltyData["shortId"] = shortIds[random.Num(len(shortIds))].(string)
	} else {
		rltyData["shortId"] = ""
	}
	serverNames, ok := rData["serverNames"].([]any)
	if ok && len(serverNames) > 0 {
		rltyData["serverName"] = serverNames[random.Num(len(serverNames))].(string)
	} else {
		rltyData["serverName"] = ""
	}

	return rltyData
}

func (s *SubJsonService) genVnext(inbound *model.Inbound, streamSettings json_util.RawMessage, client model.Client) json_util.RawMessage {
	outbound := Outbound{}

	outbound.Protocol = string(inbound.Protocol)
	outbound.Tag = "proxy"
	if s.mux != "" {
		outbound.Mux = json_util.RawMessage(s.mux)
	}
	outbound.StreamSettings = streamSettings

	security := client.Security
	if security == "" {
		security = "auto"
	}
	outbound.Settings = map[string]any{
		"address":  inbound.Listen,
		"port":     inbound.Port,
		"id":       client.ID,
		"security": security,
		"level":    8,
	}

	result, _ := json.MarshalIndent(outbound, "", "  ")
	return result
}

func (s *SubJsonService) genVless(inbound *model.Inbound, streamSettings json_util.RawMessage, client model.Client) json_util.RawMessage {
	outbound := Outbound{}
	outbound.Protocol = string(inbound.Protocol)
	outbound.Tag = "proxy"
	if s.mux != "" {
		outbound.Mux = json_util.RawMessage(s.mux)
	}
	outbound.StreamSettings = streamSettings

	// Add encryption for VLESS outbound from inbound settings
	var inboundSettings map[string]any
	json.Unmarshal([]byte(inbound.Settings), &inboundSettings)
	encryption, _ := inboundSettings["encryption"].(string)

	settings := map[string]any{
		"address":    inbound.Listen,
		"port":       inbound.Port,
		"id":         client.ID,
		"encryption": encryption,
		"level":      8,
	}
	if client.Flow != "" {
		settings["flow"] = client.Flow
	}
	outbound.Settings = settings
	result, _ := json.MarshalIndent(outbound, "", "  ")
	return result
}

func (s *SubJsonService) genServer(inbound *model.Inbound, streamSettings json_util.RawMessage, client model.Client) json_util.RawMessage {
	outbound := Outbound{}

	serverData := make([]ServerSetting, 1)
	serverData[0] = ServerSetting{
		Address:  inbound.Listen,
		Port:     inbound.Port,
		Level:    8,
		Password: client.Password,
	}

	if inbound.Protocol == model.Shadowsocks {
		var inboundSettings map[string]any
		json.Unmarshal([]byte(inbound.Settings), &inboundSettings)
		method, _ := inboundSettings["method"].(string)
		serverData[0].Method = method

		// server password in multi-user 2022 protocols
		if strings.HasPrefix(method, "2022") {
			if serverPassword, ok := inboundSettings["password"].(string); ok {
				serverData[0].Password = fmt.Sprintf("%s:%s", serverPassword, client.Password)
			}
		}
	}

	outbound.Protocol = string(inbound.Protocol)
	outbound.Tag = "proxy"
	if s.mux != "" {
		outbound.Mux = json_util.RawMessage(s.mux)
	}
	outbound.StreamSettings = streamSettings

	settings := map[string]any{
		"address":  serverData[0].Address,
		"port":     serverData[0].Port,
		"password": serverData[0].Password,
		"level":    8,
	}
	if inbound.Protocol == model.Shadowsocks {
		settings["method"] = serverData[0].Method
	}
	outbound.Settings = settings

	result, _ := json.MarshalIndent(outbound, "", "  ")
	return result
}

func (s *SubJsonService) genHy(inbound *model.Inbound, newStream map[string]any, client model.Client) json_util.RawMessage {
	outbound := Outbound{}

	outbound.Protocol = string(inbound.Protocol)
	outbound.Tag = "proxy"

	if s.mux != "" {
		outbound.Mux = json_util.RawMessage(s.mux)
	}

	var settings, stream map[string]any
	json.Unmarshal([]byte(inbound.Settings), &settings)
	version, _ := settings["version"].(float64)
	outbound.Settings = map[string]any{
		"version": int(version),
		"address": inbound.Listen,
		"port":    inbound.Port,
	}

	json.Unmarshal([]byte(inbound.StreamSettings), &stream)
	hyStream := stream["hysteriaSettings"].(map[string]any)
	outHyStream := map[string]any{
		"version": int(version),
		"auth":    client.Auth,
	}
	if udpIdleTimeout, ok := hyStream["udpIdleTimeout"].(float64); ok {
		outHyStream["udpIdleTimeout"] = int(udpIdleTimeout)
	}
	if masquerade, ok := hyStream["masquerade"].(map[string]any); ok {
		outHyStream["masquerade"] = masquerade
	}
	newStream["hysteriaSettings"] = outHyStream

	if finalmask, ok := hyStream["finalmask"].(map[string]any); ok {
		newStream["finalmask"] = mergeFinalMask(newStream["finalmask"], finalmask)
	}

	newStream["network"] = "hysteria"
	newStream["security"] = "tls"

	outbound.StreamSettings, _ = json.MarshalIndent(newStream, "", "  ")

	result, _ := json.MarshalIndent(outbound, "", "  ")
	return result
}

func mergeFinalMask(base any, extra map[string]any) map[string]any {
	merged := map[string]any{}
	if baseMap, ok := base.(map[string]any); ok {
		for key, value := range baseMap {
			switch key {
			case "tcp", "udp":
				if masks, ok := value.([]any); ok {
					merged[key] = append([]any(nil), masks...)
				}
			default:
				merged[key] = value
			}
		}
	}

	for key, value := range extra {
		switch key {
		case "tcp", "udp":
			baseMasks, _ := merged[key].([]any)
			extraMasks, _ := value.([]any)
			if len(extraMasks) > 0 {
				merged[key] = append(baseMasks, extraMasks...)
			}
		case "quicParams":
			if _, exists := merged[key]; !exists {
				merged[key] = value
			}
		default:
			merged[key] = value
		}
	}

	return merged
}

type Outbound struct {
	Protocol       string               `json:"protocol"`
	Tag            string               `json:"tag"`
	StreamSettings json_util.RawMessage `json:"streamSettings"`
	Mux            json_util.RawMessage `json:"mux,omitempty"`
	Settings       map[string]any       `json:"settings,omitempty"`
}

type ServerSetting struct {
	Password string `json:"password"`
	Level    int    `json:"level"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Flow     string `json:"flow,omitempty"`
	Method   string `json:"method,omitempty"`
}

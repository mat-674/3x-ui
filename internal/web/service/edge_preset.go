package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/util/random"
)

// PerimeterPreset describes the single-port arrangement to build: one
// VLESS+REALITY inbound on 443 whose fallback is the nginx perimeter, plus the
// perimeter settings that make that fallback land on a real website.
type PerimeterPreset struct {
	Domain string `json:"domain" example:"site.example"`
	// Listen is the inbound's listen address; empty means every interface, which
	// is what a public 443 needs.
	Listen string `json:"listen" example:""`
	Port   int    `json:"port" example:"443"`
	Remark string `json:"remark" example:"vless-reality-443"`
	Tag    string `json:"tag" example:"inbound-443"`
}

// PerimeterPresetResult reports what was created, so the UI can show the client
// link and name the next step.
type PerimeterPresetResult struct {
	InboundId int    `json:"inboundId" example:"3"`
	Tag       string `json:"tag" example:"inbound-443"`
	Domain    string `json:"domain" example:"site.example"`
	Dest      string `json:"dest" example:"127.0.0.1:8443"`
	Xver      int    `json:"xver" example:"1"`
	PublicKey string `json:"publicKey" example:"..."`
}

// BuildPerimeterPreset creates the REALITY inbound and points the perimeter's
// certificate and server names at the same domain.
//
// The inbound is stored exactly as a client sees it — TLS on 443 with the real
// domain as its server name — while REALITY answers every client that does not
// authenticate with the perimeter's website. That is the whole trick, so the two
// halves must be created together or neither works.
func (s *SettingService) BuildPerimeterPreset(preset PerimeterPreset) (*PerimeterPresetResult, error) {
	domain := strings.ToLower(strings.TrimSpace(preset.Domain))
	if domain == "" {
		return nil, common.NewError("a domain is required: it is the certificate name and the REALITY server name")
	}
	if preset.Port == 0 {
		preset.Port = 443
	}
	remark := strings.TrimSpace(preset.Remark)
	if remark == "" {
		remark = "vless-reality-" + fmt.Sprint(preset.Port)
	}
	tag := strings.TrimSpace(preset.Tag)
	if tag == "" {
		tag = fmt.Sprintf("inbound-%d", preset.Port)
	}

	// The perimeter must already know the same domain and port, or REALITY's
	// fallback would hand visitors to a server answering for another name.
	edge, err := s.GetEdgeSettings()
	if err != nil {
		return nil, err
	}
	if !edge.Enable {
		return nil, common.NewError("enable the nginx perimeter first: REALITY needs a fallback that answers like a real site")
	}
	dest := fmt.Sprintf("%s:%d", edge.Listen, edge.Port)

	keys, err := (&ServerService{}).GetNewX25519Cert()
	if err != nil {
		return nil, common.NewErrorf("generate REALITY keys: %v", err)
	}
	keyPair, ok := keys.(map[string]any)
	if !ok {
		return nil, common.NewError("unexpected key generator output")
	}
	privateKey, _ := keyPair["privateKey"].(string)
	publicKey, _ := keyPair["publicKey"].(string)
	if privateKey == "" || publicKey == "" {
		return nil, common.NewError("REALITY key generation produced no usable pair")
	}

	xver := 0
	if edge.ProxyProtocol {
		xver = 1
	}
	stream := map[string]any{
		"network":  "tcp",
		"security": "reality",
		"realitySettings": map[string]any{
			"show":        false,
			"dest":        dest,
			"xver":        xver,
			"serverNames": []string{domain},
			"privateKey":  privateKey,
			"shortIds":    []string{random.Seq(8)},
		},
	}
	streamJSON, err := json.Marshal(stream)
	if err != nil {
		return nil, err
	}
	settings := map[string]any{
		"clients":    []any{},
		"decryption": "none",
	}
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return nil, err
	}

	inbound := &model.Inbound{
		Enable:         true,
		Remark:         remark,
		Listen:         strings.TrimSpace(preset.Listen),
		Port:           preset.Port,
		Protocol:       model.VLESS,
		Settings:       string(settingsJSON),
		StreamSettings: string(streamJSON),
		Tag:            tag,
		Sniffing:       `{"enabled":true,"destOverride":["http","tls","quic"]}`,
	}
	created, _, err := (&InboundService{}).AddInbound(inbound)
	if err != nil {
		return nil, err
	}

	// Keep the two halves pointing at the same name: the certificate must cover
	// the domain REALITY advertises, or a client validates against a mismatch.
	if err := s.setString("edgeServerNames", domain); err != nil {
		return nil, err
	}
	return &PerimeterPresetResult{
		InboundId: created.Id,
		Tag:       created.Tag,
		Domain:    domain,
		Dest:      dest,
		Xver:      xver,
		PublicKey: publicKey,
	}, nil
}

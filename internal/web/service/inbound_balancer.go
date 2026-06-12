package service

import (
	"encoding/json"
	"strconv"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// balancerEligibleProtocols are the protocols whose inbounds can be members of
// a subscription balancer — the same set the JSON subscription can emit as
// outbounds.
var balancerEligibleProtocols = map[model.Protocol]bool{
	model.VMESS:       true,
	model.VLESS:       true,
	model.Trojan:      true,
	model.Shadowsocks: true,
	model.Hysteria:    true,
}

// validateBalancerMembers loads the chosen member inbounds and rejects the set
// unless it has at least two members, all of which are real, balancer-eligible
// inbounds owned by the user.
func (s *InboundService) validateBalancerMembers(userId int, members []int) error {
	if len(members) < 2 {
		return common.NewError("a balancer needs at least two member inbounds")
	}
	db := database.GetDB()
	var rows []model.Inbound
	if err := db.Model(model.Inbound{}).Where("id IN ?", members).Find(&rows).Error; err != nil {
		return err
	}
	byId := make(map[int]model.Inbound, len(rows))
	for _, r := range rows {
		byId[r.Id] = r
	}
	for _, id := range members {
		ib, ok := byId[id]
		if !ok {
			return common.NewError("member inbound not found:", id)
		}
		if ib.UserId != userId {
			return common.NewError("member inbound not owned by user:", id)
		}
		if !balancerEligibleProtocols[ib.Protocol] {
			return common.NewError("member inbound protocol not eligible for balancer:", string(ib.Protocol))
		}
	}
	return nil
}

// prepareBalancer normalizes a balancer inbound before persisting: forces the
// pseudo-protocol, validates members, re-serializes settings, and clears fields
// that only apply to real xray inbounds (it never runs on xray-core).
//
// keepSettings carries the row's current settings JSON on update so the
// attached-client list ("clients") written by the standard client-attach flow
// survives an edit of the balancer's members/probe.
func (s *InboundService) prepareBalancer(inbound *model.Inbound, keepSettings string) error {
	settings, err := model.ParseBalancerSettings(inbound.Settings)
	if err != nil {
		return common.NewError("invalid balancer settings:", err)
	}
	if err := s.validateBalancerMembers(inbound.UserId, settings.Members); err != nil {
		return err
	}
	// Merge the balancer config into the existing settings object so the
	// "clients" array (assigned clients) is preserved.
	merged := map[string]any{}
	if keepSettings != "" {
		_ = json.Unmarshal([]byte(keepSettings), &merged)
	}
	var balancerObj map[string]any
	_ = json.Unmarshal([]byte(settings.JSON()), &balancerObj)
	merged["balancer"] = balancerObj["balancer"]
	// Always keep a clients array so the standard client-attach flow (which
	// does oldSettings["clients"].([]any)) doesn't trip over a fresh balancer.
	if _, ok := merged["clients"].([]any); !ok {
		merged["clients"] = []any{}
	}
	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}

	inbound.Protocol = model.Balancer
	inbound.Settings = string(out)
	inbound.StreamSettings = ""
	inbound.Sniffing = ""
	inbound.Listen = ""
	inbound.Port = 0
	inbound.NodeID = nil
	inbound.ClientStats = nil
	return nil
}

// AddBalancer persists a new balancer inbound. Unlike AddInbound it skips port
// conflict checks, client handling, and the xray runtime push — a balancer is a
// panel-only grouping that lives purely in the subscription layer.
func (s *InboundService) AddBalancer(inbound *model.Inbound) (*model.Inbound, error) {
	if err := s.prepareBalancer(inbound, ""); err != nil {
		return inbound, err
	}
	inbound.SubSortIndex = normalizeSubSortIndex(inbound.SubSortIndex)

	tag, err := s.generateBalancerTag(0)
	if err != nil {
		return inbound, err
	}
	inbound.Tag = tag

	db := database.GetDB()
	if err := db.Omit("ClientStats").Create(inbound).Error; err != nil {
		return inbound, err
	}
	return inbound, nil
}

// UpdateBalancer rewrites an existing balancer's members/probe/remark. It
// preserves the row's tag and never touches xray.
func (s *InboundService) UpdateBalancer(inbound *model.Inbound) (*model.Inbound, error) {
	existing, err := s.GetInbound(inbound.Id)
	if err != nil {
		return inbound, err
	}
	if existing.Protocol != model.Balancer {
		return inbound, common.NewError("inbound is not a balancer:", inbound.Id)
	}
	inbound.UserId = existing.UserId
	if err := s.prepareBalancer(inbound, existing.Settings); err != nil {
		return inbound, err
	}
	inbound.Tag = existing.Tag
	inbound.SubSortIndex = normalizeSubSortIndex(inbound.SubSortIndex)

	db := database.GetDB()
	if err := db.Model(model.Inbound{}).Where("id = ?", inbound.Id).
		Omit("ClientStats").
		Select("remark", "enable", "settings", "sub_sort_index", "protocol",
			"stream_settings", "sniffing", "listen", "port", "node_id").
		Updates(inbound).Error; err != nil {
		return inbound, err
	}
	return inbound, nil
}

// BalancerClientsForMember returns the clients assigned to any enabled balancer
// whose members include memberId. The Xray config builder folds these into the
// member inbound's runtime user list so the member server accepts clients that
// are attached only to the balancer (and therefore never get their own
// client_inbounds row on the member).
func (s *InboundService) BalancerClientsForMember(memberId int) []model.Client {
	db := database.GetDB()
	var balancers []*model.Inbound
	if err := db.Model(model.Inbound{}).
		Where("protocol = ? AND enable = ?", model.Balancer, true).
		Find(&balancers).Error; err != nil {
		return nil
	}
	var out []model.Client
	for _, b := range balancers {
		bs, err := model.ParseBalancerSettings(b.Settings)
		if err != nil || !bs.Contains(memberId) {
			continue
		}
		clients, err := s.clientService.ListForInbound(nil, b.Id)
		if err != nil {
			continue
		}
		out = append(out, clients...)
	}
	return out
}

// generateBalancerTag picks a unique tag of the form "balancer-N" for a new
// balancer row.
func (s *InboundService) generateBalancerTag(ignoreId int) (string, error) {
	for i := 1; i < 1000; i++ {
		candidate := "balancer-" + strconv.Itoa(i)
		exists, err := s.tagExists(candidate, ignoreId)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", common.NewError("could not pick a unique balancer tag")
}

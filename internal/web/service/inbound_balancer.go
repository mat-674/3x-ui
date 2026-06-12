package service

import (
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
func (s *InboundService) prepareBalancer(inbound *model.Inbound) error {
	settings, err := model.ParseBalancerSettings(inbound.Settings)
	if err != nil {
		return common.NewError("invalid balancer settings:", err)
	}
	if err := s.validateBalancerMembers(inbound.UserId, settings.Members); err != nil {
		return err
	}
	inbound.Protocol = model.Balancer
	inbound.Settings = settings.JSON()
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
	if err := s.prepareBalancer(inbound); err != nil {
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
	if err := s.prepareBalancer(inbound); err != nil {
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

// BalancerMemberClient is one candidate client the balancer can be shown to,
// surfaced to the frontend's "selected" visibility picker. One entry per
// distinct subscription ID, with the member-client emails that share it for a
// human-readable label.
type BalancerMemberSub struct {
	SubID  string   `json:"subId"`
	Emails []string `json:"emails"`
}

// BalancerMemberSubs returns the distinct subscription IDs present on the given
// member inbounds, so the admin can pick which subscriptions see the balancer.
// Only balancer-eligible member inbounds owned by the user are considered.
// Clients without a subId are skipped — they can't be targeted by subscription.
func (s *InboundService) BalancerMemberSubs(userId int, memberIds []int) ([]BalancerMemberSub, error) {
	if len(memberIds) == 0 {
		return nil, nil
	}
	db := database.GetDB()
	var rows []model.Inbound
	if err := db.Model(model.Inbound{}).Where("id IN ? AND user_id = ?", memberIds, userId).Find(&rows).Error; err != nil {
		return nil, err
	}
	bySub := make(map[string][]string)
	order := make([]string, 0)
	seenEmail := make(map[string]struct{})
	for i := range rows {
		ib := &rows[i]
		if !balancerEligibleProtocols[ib.Protocol] {
			continue
		}
		clients, err := s.GetClients(ib)
		if err != nil {
			continue
		}
		for _, c := range clients {
			if c.SubID == "" {
				continue
			}
			if _, ok := bySub[c.SubID]; !ok {
				order = append(order, c.SubID)
			}
			// Dedup emails within a subId so a client attached to several
			// member inbounds isn't listed more than once in the label.
			key := c.SubID + "\x00" + c.Email
			if c.Email != "" {
				if _, dup := seenEmail[key]; !dup {
					seenEmail[key] = struct{}{}
					bySub[c.SubID] = append(bySub[c.SubID], c.Email)
				}
			} else if _, ok := bySub[c.SubID]; !ok {
				bySub[c.SubID] = nil
			}
		}
	}
	out := make([]BalancerMemberSub, 0, len(order))
	for _, subId := range order {
		out = append(out, BalancerMemberSub{SubID: subId, Emails: bySub[subId]})
	}
	return out, nil
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

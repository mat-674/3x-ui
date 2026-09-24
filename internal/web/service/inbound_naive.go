package service

import (
	"context"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/naive"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

func (s *InboundService) DesiredNaiveInstances() ([]naive.Instance, error) {
	db := database.GetDB()
	var inbounds []*model.Inbound
	if err := db.Model(model.Inbound{}).
		Where("protocol = ? AND enable = ? AND node_id IS NULL", model.Naive, true).
		Find(&inbounds).Error; err != nil {
		return nil, err
	}
	if len(inbounds) == 0 {
		return nil, nil
	}

	ids := make([]int, 0, len(inbounds))
	for _, ib := range inbounds {
		ids = append(ids, ib.Id)
	}
	var disabledRows []xray.ClientTraffic
	if err := db.Model(xray.ClientTraffic{}).
		Where("inbound_id IN ? AND enable = ?", ids, false).
		Select("inbound_id", "email").
		Find(&disabledRows).Error; err != nil {
		return nil, err
	}
	disabled := make(map[int]map[string]struct{}, len(disabledRows))
	for _, row := range disabledRows {
		if disabled[row.InboundId] == nil {
			disabled[row.InboundId] = map[string]struct{}{}
		}
		disabled[row.InboundId][row.Email] = struct{}{}
	}

	instances := make([]naive.Instance, 0, len(inbounds))
	for _, ib := range inbounds {
		inst, ok := naive.InstanceFromInbound(ib)
		if !ok {
			continue
		}
		if off := disabled[ib.Id]; len(off) > 0 {
			kept := make([]naive.Client, 0, len(inst.Clients))
			for _, client := range inst.Clients {
				if _, skip := off[client.Email]; !skip {
					kept = append(kept, client)
				}
			}
			inst.Clients = kept
		}
		if len(inst.Clients) == 0 {
			continue
		}
		instances = append(instances, inst)
	}
	return instances, nil
}

func (s *InboundService) applyLocalNaive(inboundID int) {
	inbound, err := s.GetInbound(inboundID)
	if err != nil || inbound == nil || inbound.Protocol != model.Naive || inbound.NodeID != nil {
		return
	}
	rt, err := s.runtimeFor(inbound)
	if err != nil {
		return
	}
	payload := inbound
	if inbound.Enable {
		if built, buildErr := s.buildInboundForLocalRuntime(database.GetDB(), inbound); buildErr == nil {
			payload = built
		}
	}
	if err := rt.UpdateInbound(context.Background(), inbound, payload); err != nil {
		logger.Debug("naive: immediate client apply failed for inbound", inboundID, ":", err)
	}
}

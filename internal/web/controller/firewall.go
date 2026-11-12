package controller

import (
	"errors"
	"io"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// FirewallController exposes the host firewall: the rendered ruleset, what is
// loaded, and the apply/confirm lifecycle whose window is the only way back.
type FirewallController struct {
	firewallService *service.FirewallService
}

// NewFirewallController registers the firewall routes on the authenticated API group.
func NewFirewallController(g *gin.RouterGroup) *FirewallController {
	a := &FirewallController{firewallService: service.NewFirewallService()}
	fw := g.Group("/firewall")
	fw.GET("/status", a.status)
	fw.GET("/config", a.previewConfig)
	fw.POST("/apply", a.apply)
	fw.POST("/confirm", a.confirm)
	return a
}

func (a *FirewallController) status(c *gin.Context) {
	status, err := a.firewallService.Status(c.Request.Context())
	jsonObj(c, status, err)
}

// previewConfig answers the exact ruleset Apply would load, so an operator can
// read it before it can affect anything live.
func (a *FirewallController) previewConfig(c *gin.Context) {
	text, err := a.firewallService.Preview()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, gin.H{"config": text}, nil)
}

// firewallApplyRequest carries the required rollback window. Without one Apply
// has no way back, so an absent window is a refusal, never a default.
type firewallApplyRequest struct {
	ConfirmInSeconds int `json:"confirmInSeconds"`
}

func (a *FirewallController) apply(c *gin.Context) {
	var req firewallApplyRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	id, err := a.firewallService.Apply(c.Request.Context(), time.Duration(req.ConfirmInSeconds)*time.Second)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsgObj(c, I18nWeb(c, "success"), gin.H{
		"deadlineId":       id,
		"confirmInSeconds": req.ConfirmInSeconds,
	}, nil)
}

type firewallConfirmRequest struct {
	DeadlineID string `json:"deadlineId"`
}

// confirm retires the rollback armed by apply, once the operator has proven the
// panel and SSH still answer on the new ruleset.
func (a *FirewallController) confirm(c *gin.Context) {
	var req firewallConfirmRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	if err := a.firewallService.Confirm(req.DeadlineID); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, I18nWeb(c, "success"), nil)
}

package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// EdgeController exposes the nginx perimeter that sits behind xray's REALITY
// fallback: its generated config, its state, and the apply/stop lifecycle.
type EdgeController struct{}

// NewEdgeController registers the perimeter routes on the authenticated API group.
func NewEdgeController(g *gin.RouterGroup) *EdgeController {
	a := &EdgeController{}
	edge := g.Group("/edge")
	edge.GET("/status", a.status)
	edge.GET("/config", a.previewConfig)
	edge.POST("/apply", a.apply)
	edge.POST("/stop", a.stop)
	edge.POST("/preset", a.preset)
	edge.POST("/install-nginx", a.installNginx)
	return a
}

// installNginx installs the binary on demand, which is what makes the perimeter
// usable without an install-time flag: enable it and the panel adds what it needs.
func (a *EdgeController) installNginx(c *gin.Context) {
	report, err := (&service.SettingService{}).InstallEdgeNginx(c.Request.Context())
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, report, nil)
}

// preset builds the whole single-port arrangement: the REALITY inbound whose
// fallback is the perimeter, plus the perimeter's own domain. Doing it in one
// call matters because half of it is a dead proxy and the other half is a
// website with no proxy behind it.
func (a *EdgeController) preset(c *gin.Context) {
	var body service.PerimeterPreset
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	result, err := (&service.SettingService{}).BuildPerimeterPreset(body)
	jsonObj(c, result, err)
}

func (a *EdgeController) status(c *gin.Context) {
	status, err := (&service.SettingService{}).EdgeStatus()
	jsonObj(c, status, err)
}

// previewConfig answers the exact text nginx would be given, so an operator can
// compare it against what the perimeter actually loaded.
func (a *EdgeController) previewConfig(c *gin.Context) {
	text, err := (&service.SettingService{}).PreviewEdgeConfig()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, gin.H{"config": text}, nil)
}

func (a *EdgeController) apply(c *gin.Context) {
	if err := (&service.SettingService{}).ApplyEdge(c.Request.Context()); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.settings.edge.applied"), nil)
}

func (a *EdgeController) stop(c *gin.Context) {
	mgr, err := (&service.SettingService{}).EdgeManager()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	if err := mgr.Stop(c.Request.Context()); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "msg": "", "obj": nil})
}

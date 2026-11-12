package controller

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// FrontController manages the decoy websites the panel publishes: upload a
// zipped site, list what is stored, pick the one served, delete the rest.
type FrontController struct {
	FrontSiteService service.FrontSiteService
}

// NewFrontController registers the front routes on the authenticated API group,
// so they inherit its token scopes and CSRF check.
func NewFrontController(g *gin.RouterGroup) *FrontController {
	a := &FrontController{}
	sites := g.Group("/front/sites")
	sites.GET("", a.listSites)
	sites.POST("/upload", a.uploadSite)
	sites.POST("/:id/activate", a.activateSite)
	sites.POST("/:id/delete", a.deleteSite)
	return a
}

func (a *FrontController) listSites(c *gin.Context) {
	sites, err := a.FrontSiteService.List()
	jsonObj(c, sites, err)
}

// uploadSite reads multipart "file" and form "name". The body is capped before
// the multipart parser can spool an oversized upload to disk.
func (a *FrontController) uploadSite(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxFrontSiteArchiveBytes+(1<<20))
	header, err := c.FormFile("file")
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), fmt.Errorf("upload requires a file field: %w", err))
		return
	}
	archive, err := header.Open()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), fmt.Errorf("open uploaded archive: %w", err))
		return
	}
	defer func() { _ = archive.Close() }()

	site, err := a.FrontSiteService.Upload(c.PostForm("name"), archive)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, site, nil)
}

func (a *FrontController) activateSite(c *gin.Context) {
	site, err := a.site(c)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	if err := a.FrontSiteService.Activate(site.Id); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	site.Active = true
	jsonObj(c, site, nil)
}

func (a *FrontController) deleteSite(c *gin.Context) {
	site, err := a.site(c)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	if err := a.FrontSiteService.Delete(site.Id); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, site, nil)
}

// site resolves the :id path parameter to a stored row, so a malformed id and an
// unknown id both answer with the documented error envelope.
func (a *FrontController) site(c *gin.Context) (*model.FrontSite, error) {
	raw := c.Param("id")
	id, err := strconv.Atoi(raw)
	if err != nil || id < 1 {
		return nil, fmt.Errorf("invalid front site id %q", raw)
	}
	return a.FrontSiteService.Get(id)
}

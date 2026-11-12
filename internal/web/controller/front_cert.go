package controller

import (
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// FrontCertController manages the certificates the TLS front presents: list,
// save from a path pair, delete, and a reload that reports what the front holds.
type FrontCertController struct {
	FrontCertService *service.FrontCertService
}

// NewFrontCertController registers the front-certificate routes on the
// authenticated API group, so they inherit its token scopes and CSRF check.
func NewFrontCertController(g *gin.RouterGroup, store *service.FrontCertService) *FrontCertController {
	if store == nil {
		store = service.DefaultFrontCertService()
	}
	a := &FrontCertController{FrontCertService: store}
	certs := g.Group("/front/certs")
	certs.GET("", a.listCerts)
	certs.POST("", a.saveCert)
	certs.POST("/reload", a.reloadCerts)
	certs.POST("/:id/delete", a.deleteCert)
	return a
}

// frontCertRequest is the JSON body of POST /front/certs. Id 0 creates a row;
// any existing id updates that row in place.
type frontCertRequest struct {
	Id        int    `json:"id"`
	Domains   string `json:"domains"`
	CertPath  string `json:"certPath"`
	KeyPath   string `json:"keyPath"`
	Issuer    string `json:"issuer"`
	AcmeEmail string `json:"acmeEmail"`
}

func (a *FrontCertController) listCerts(c *gin.Context) {
	rows, err := a.FrontCertService.ListFrontCerts()
	jsonObj(c, rows, err)
}

// saveCert validates and stores a pair; UpsertFrontCert refuses one that does
// not load, so a typo in a path never reaches the listener.
func (a *FrontCertController) saveCert(c *gin.Context) {
	var req frontCertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), fmt.Errorf("invalid front certificate body: %w", err))
		return
	}
	row, err := a.FrontCertService.SaveFrontCert(&model.FrontCert{
		Id:        req.Id,
		Domains:   req.Domains,
		CertPath:  req.CertPath,
		KeyPath:   req.KeyPath,
		Issuer:    req.Issuer,
		AcmeEmail: req.AcmeEmail,
	})
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, row, nil)
}

func (a *FrontCertController) deleteCert(c *gin.Context) {
	id, err := frontCertId(c)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	row, err := a.FrontCertService.GetFrontCert(id)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	if err := a.FrontCertService.DeleteFrontCert(id); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, row, nil)
}

// reloadCerts answers an object, not a bare success: the operator must see every
// expiry, the no-SNI default, and hear it when nothing is configured.
func (a *FrontCertController) reloadCerts(c *gin.Context) {
	report := a.FrontCertService.ReloadReport()
	payload := gin.H{
		"hasCertificate": report.HasCertificate,
		"warning":        report.Warning,
		"certificates":   frontCertSummaries(report.Certificates),
	}
	var def any
	if report.Default != nil {
		def = frontCertSummary(*report.Default)
	}
	payload["default"] = def
	jsonObj(c, payload, nil)
}

// frontCertSummaries maps the report to plain maps, so no response struct needs
// a tools/openapigen StructAllow entry.
func frontCertSummaries(summaries []service.FrontCertSummary) []gin.H {
	out := make([]gin.H, 0, len(summaries))
	for _, summary := range summaries {
		out = append(out, frontCertSummary(summary))
	}
	return out
}

func frontCertSummary(summary service.FrontCertSummary) gin.H {
	domains := summary.Domains
	if domains == nil {
		domains = []string{}
	}
	return gin.H{
		"id":       summary.Id,
		"domains":  domains,
		"mode":     summary.Mode,
		"issuer":   summary.Issuer,
		"notAfter": summary.NotAfter,
		"loaded":   summary.Loaded,
		"error":    summary.Error,
	}
}

// frontCertId resolves the :id path parameter, so a malformed id and an unknown
// id both answer with the documented error envelope.
func frontCertId(c *gin.Context) (int, error) {
	raw := c.Param("id")
	id, err := strconv.Atoi(raw)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("invalid front certificate id %q", raw)
	}
	return id, nil
}

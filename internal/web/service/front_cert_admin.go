package service

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"

	"gorm.io/gorm"
)

// The two ways a pair reaches the front: an operator-supplied file, or one this
// panel obtained from an ACME issuer.
const (
	frontCertModeFile   = "file"
	frontCertIssuerFile = "file"
)

// The admin API must invalidate the same cache the listener serves from: a
// private instance would leave a deleted certificate answering until the TTL.
var (
	frontCertStoreMu sync.RWMutex
	frontCertStore   = &FrontCertService{}
)

// DefaultFrontCertService returns the shared certificate store.
func DefaultFrontCertService() *FrontCertService {
	frontCertStoreMu.RLock()
	defer frontCertStoreMu.RUnlock()
	return frontCertStore
}

// SetDefaultFrontCertService points the admin API at the instance the listener
// uses; the panel server calls it once during startup.
func SetDefaultFrontCertService(store *FrontCertService) {
	if store == nil {
		return
	}
	frontCertStoreMu.Lock()
	frontCertStore = store
	frontCertStoreMu.Unlock()
}

// FrontCertSummary is one saved row as the listener holds it right now; Loaded
// is the only signal that a rotation left the front serving nothing.
type FrontCertSummary struct {
	Id       int
	Domains  []string
	Mode     string
	Issuer   string
	NotAfter *time.Time
	Loaded   bool
	Error    string
}

// FrontCertReloadReport answers "what does the front serve after a reload",
// including what a client sending no SNI receives and a no-material warning.
type FrontCertReloadReport struct {
	Certificates   []FrontCertSummary
	Default        *FrontCertSummary
	HasCertificate bool
	Warning        string
}

// frontCertNoMaterialWarning is the message an operator sees when the front has
// no TLS at all; it must say so in words, because an empty list reads as "fine".
const frontCertNoMaterialWarning = "the front has no certificate: no TLS handshake can succeed until a pair is saved"

// SaveFrontCert stores a row through UpsertFrontCert, so an unloadable pair is
// refused before it reaches the front and an update keeps the row identity.
func (s *FrontCertService) SaveFrontCert(row *model.FrontCert) (*model.FrontCert, error) {
	if row == nil {
		return nil, common.NewError("front certificate payload is empty")
	}
	target := row
	if row.Id > 0 {
		existing, err := s.GetFrontCert(row.Id)
		if err != nil {
			return nil, err
		}
		existing.Domains = row.Domains
		existing.CertPath = row.CertPath
		existing.KeyPath = row.KeyPath
		existing.Issuer = row.Issuer
		existing.AcmeEmail = row.AcmeEmail
		existing.Mode = row.Mode
		target = existing
	}
	target.Mode = strings.TrimSpace(target.Mode)
	if target.Mode == "" {
		target.Mode = frontCertModeFile
	}
	target.Issuer = strings.TrimSpace(target.Issuer)
	if target.Issuer == "" {
		target.Issuer = frontCertIssuerFile
	}
	target.CertPath = strings.TrimSpace(target.CertPath)
	target.KeyPath = strings.TrimSpace(target.KeyPath)
	target.AcmeEmail = strings.TrimSpace(target.AcmeEmail)
	if err := s.UpsertFrontCert(target); err != nil {
		return nil, err
	}
	return target, nil
}

// GetFrontCert returns one row by id.
func (s *FrontCertService) GetFrontCert(id int) (*model.FrontCert, error) {
	db := database.GetDB()
	if db == nil {
		return nil, common.NewError("database is not initialised")
	}
	var row model.FrontCert
	if err := db.First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, common.NewErrorf("front certificate %d not found", id)
		}
		return nil, err
	}
	return &row, nil
}

// ReloadReport forces a reload and describes the result. The expiry comes from
// the loaded leaf, because a rotated file's new expiry is not in the stored row.
func (s *FrontCertService) ReloadReport() FrontCertReloadReport {
	entries := s.Reload()
	loaded := make(map[int]*tls.Certificate, len(entries))
	for _, entry := range entries {
		if entry.cert != nil {
			loaded[entry.certId] = entry.cert
		}
	}

	report := FrontCertReloadReport{Certificates: []FrontCertSummary{}}
	rows, err := s.ListFrontCerts()
	if err != nil {
		report.Warning = "front certificates could not be read: " + err.Error()
		return report
	}
	for _, row := range rows {
		summary := FrontCertSummary{
			Id:      row.Id,
			Domains: splitDomains(row.Domains),
			Mode:    row.Mode,
			Issuer:  row.Issuer,
		}
		if cert, ok := loaded[row.Id]; ok {
			summary.Loaded = true
			summary.NotAfter = frontCertNotAfter(cert)
		} else {
			summary.NotAfter = row.NotAfter
			summary.Error = frontCertLoadError(row)
		}
		report.Certificates = append(report.Certificates, summary)
	}
	for i := range report.Certificates {
		if report.Certificates[i].Loaded {
			def := report.Certificates[i]
			report.Default = &def
			report.HasCertificate = true
			break
		}
	}
	if !report.HasCertificate {
		report.Warning = frontCertNoMaterialWarning
	}
	return report
}

// frontCertLoadError explains why a stored row is not being served.
func frontCertLoadError(row model.FrontCert) string {
	if _, err := tls.LoadX509KeyPair(row.CertPath, row.KeyPath); err != nil {
		return err.Error()
	}
	return "the front did not load this pair"
}

// frontCertNotAfter reads the leaf expiry out of loaded material.
func frontCertNotAfter(cert *tls.Certificate) *time.Time {
	if cert == nil || len(cert.Certificate) == 0 {
		return nil
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil
	}
	notAfter := leaf.NotAfter
	return &notAfter
}

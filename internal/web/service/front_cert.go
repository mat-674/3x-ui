package service

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// frontCertCacheTTL is how often file-backed material is re-read. Certificates
// are rotated by replacing the file, not by restarting the panel, so the mtime
// check has to be cheap and frequent.
const frontCertCacheTTL = 30 * time.Second

// FrontCertService loads the TLS material the front presents, keyed by server
// name, and re-reads a file-backed pair when its file changes on disk.
type FrontCertService struct {
	mu        sync.RWMutex
	loaded    []frontCertEntry
	loadedAt  time.Time
	lastCheck time.Time
}

type frontCertEntry struct {
	domains []string
	cert    *tls.Certificate
	modTime time.Time
	certId  int
}

// Load returns the current certificate set, reloading from disk when the cache
// is cold or a backing file changed.
func (s *FrontCertService) Load() []frontCertEntry {
	s.mu.RLock()
	fresh := time.Since(s.lastCheck) < frontCertCacheTTL
	entries := s.loaded
	s.mu.RUnlock()
	if fresh {
		return entries
	}
	return s.Reload()
}

// Reload re-reads every certificate row. A row that fails to load is kept out
// of the set but never destroys the previous good material for its domain, so a
// half-written file during rotation cannot take the site down.
func (s *FrontCertService) Reload() []frontCertEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastCheck = time.Now()
	db := database.GetDB()
	if db == nil {
		return s.loaded
	}
	var rows []model.FrontCert
	if err := db.Order("id asc").Find(&rows).Error; err != nil {
		logger.Warning("front certificates could not be read:", err)
		return s.loaded
	}
	previous := make(map[int]*tls.Certificate, len(s.loaded))
	previousMod := make(map[int]time.Time, len(s.loaded))
	for _, e := range s.loaded {
		previous[e.certId] = e.cert
		previousMod[e.certId] = e.modTime
	}
	next := make([]frontCertEntry, 0, len(rows))
	for _, row := range rows {
		modTime, cert := statFrontCertFiles(row)
		if cert == nil && modTime.IsZero() {
			continue
		}
		if cached, ok := previous[row.Id]; ok && cert == nil && modTime.Equal(previousMod[row.Id]) {
			next = append(next, frontCertEntry{domains: splitDomains(row.Domains), cert: cached, modTime: modTime, certId: row.Id})
			continue
		}
		loaded, err := tls.LoadX509KeyPair(row.CertPath, row.KeyPath)
		if err != nil {
			logger.Warningf("front certificate %d (%s) failed to load, keeping the previous one: %v", row.Id, row.Domains, err)
			if cached, ok := previous[row.Id]; ok {
				next = append(next, frontCertEntry{domains: splitDomains(row.Domains), cert: cached, modTime: previousMod[row.Id], certId: row.Id})
			}
			continue
		}
		next = append(next, frontCertEntry{domains: splitDomains(row.Domains), cert: &loaded, modTime: modTime, certId: row.Id})
	}
	s.loaded = next
	s.loadedAt = time.Now()
	return next
}

// statFrontCertFiles reports the newest mtime of a pair, so the cache notices a
// rotation without parsing the certificate every time.
func statFrontCertFiles(row model.FrontCert) (time.Time, *tls.Certificate) {
	var newest time.Time
	for _, path := range []string{row.CertPath, row.KeyPath} {
		if strings.TrimSpace(path) == "" {
			return time.Time{}, nil
		}
		info, err := os.Stat(path)
		if err != nil {
			return time.Time{}, nil
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest, nil
}

func splitDomains(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if d := strings.ToLower(strings.TrimSpace(f)); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// CertificateFor returns the material covering sni, preferring an exact match
// over a wildcard so an explicit row always wins.
func (s *FrontCertService) CertificateFor(sni string) *tls.Certificate {
	entries := s.Load()
	sni = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(sni), "."))
	if sni == "" {
		return nil
	}
	var wildcard *tls.Certificate
	for _, e := range entries {
		if e.cert == nil {
			continue
		}
		for _, d := range e.domains {
			switch {
			case d == sni:
				return e.cert
			case strings.HasPrefix(d, "*.") && wildcardCovers(d, sni):
				if wildcard == nil {
					wildcard = e.cert
				}
			}
		}
	}
	return wildcard
}

// Fallback returns the first loadable certificate, presented to clients that
// sent no SNI at all.
func (s *FrontCertService) Fallback() *tls.Certificate {
	for _, e := range s.Load() {
		if e.cert != nil {
			return e.cert
		}
	}
	return nil
}

// wildcardCovers reports whether a "*.example.com" pattern matches host, which
// per TLS rules covers exactly one extra label.
func wildcardCovers(pattern, host string) bool {
	suffix := strings.TrimPrefix(pattern, "*")
	if !strings.HasSuffix(host, suffix) {
		return false
	}
	label := strings.TrimSuffix(host, suffix)
	return label != "" && !strings.Contains(label, ".")
}

// UpsertFrontCert stores one certificate row after validating that the pair on
// disk actually loads, so a bad path never reaches the front.
func (s *FrontCertService) UpsertFrontCert(row *model.FrontCert) error {
	row.Domains = strings.Join(splitDomains(row.Domains), ",")
	if row.Domains == "" {
		return common.NewError("front certificate needs at least one domain name")
	}
	if strings.TrimSpace(row.CertPath) == "" || strings.TrimSpace(row.KeyPath) == "" {
		return common.NewError("front certificate needs both a certificate and a key path")
	}
	loaded, err := tls.LoadX509KeyPair(row.CertPath, row.KeyPath)
	if err != nil {
		return common.NewErrorf("front certificate does not load: %v", err)
	}
	if len(loaded.Certificate) == 0 {
		return common.NewError("front certificate file holds no certificate")
	}
	if leaf, parseErr := x509.ParseCertificate(loaded.Certificate[0]); parseErr == nil {
		notAfter := leaf.NotAfter
		row.NotAfter = &notAfter
	}
	db := database.GetDB()
	if db == nil {
		return common.NewError("database is not initialised")
	}
	if err := db.Save(row).Error; err != nil {
		return err
	}
	s.mu.Lock()
	s.lastCheck = time.Time{}
	s.mu.Unlock()
	return nil
}

// DeleteFrontCert drops a row and refreshes the cache.
func (s *FrontCertService) DeleteFrontCert(id int) error {
	db := database.GetDB()
	if db == nil {
		return common.NewError("database is not initialised")
	}
	if err := db.Delete(&model.FrontCert{}, id).Error; err != nil {
		return err
	}
	s.mu.Lock()
	s.lastCheck = time.Time{}
	s.mu.Unlock()
	return nil
}

// ListFrontCerts returns every stored row for the settings UI.
func (s *FrontCertService) ListFrontCerts() ([]model.FrontCert, error) {
	db := database.GetDB()
	if db == nil {
		return nil, common.NewError("database is not initialised")
	}
	var rows []model.FrontCert
	err := db.Order("id asc").Find(&rows).Error
	return rows, err
}

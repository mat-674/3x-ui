package model

import "time"

// FrontSite is an uploaded decoy website served by the TLS front on the panel's
// public port. Files live on disk under Dir; only metadata is stored here, so a
// panel backup never carries a whole website.
type FrontSite struct {
	Id        int       `json:"id" gorm:"primaryKey;autoIncrement" example:"1"`
	Name      string    `json:"name" gorm:"unique" example:"corporate"`
	Dir       string    `json:"dir" example:"/etc/x-ui/front/sites/1"`
	EntryFile string    `json:"entryFile" example:"index.html"`
	Bytes     int64     `json:"bytes" gorm:"default:0" example:"204800"`
	FileCount int       `json:"fileCount" gorm:"default:0" example:"42"`
	Sha256    string    `json:"sha256" example:"9f2c..."`
	Active    bool      `json:"active" gorm:"default:false" example:"true"`
	CreatedAt time.Time `json:"createdAt" example:"2026-01-02T15:04:05Z"`
}

func (FrontSite) TableName() string { return "front_sites" }

// FrontCert is one TLS certificate the front presents, keyed by the server
// names it covers. Private keys stay on disk (0600): the rows carry paths only,
// the same split node mTLS material uses, so keys never reach settings export.
type FrontCert struct {
	Id       int    `json:"id" gorm:"primaryKey;autoIncrement" example:"1"`
	Domains  string `json:"domains" example:"panel.example.com"`
	Mode     string `json:"mode" gorm:"default:file" example:"file"`
	CertPath string `json:"certPath" example:"/etc/x-ui/front/certs/panel.crt"`
	KeyPath  string `json:"keyPath" example:"/etc/x-ui/front/certs/panel.key"`
	// Issuer is "file" for an operator-supplied pair or "acme" for one this
	// panel obtained; AcmeEmail is the account contact used for the latter.
	Issuer      string     `json:"issuer" gorm:"default:file" example:"acme"`
	AcmeEmail   string     `json:"acmeEmail" example:"admin@example.com"`
	NotAfter    *time.Time `json:"notAfter,omitempty" example:"2026-04-02T15:04:05Z"`
	LastError   string     `json:"lastError" example:""`
	LastAttempt *time.Time `json:"lastAttempt,omitempty" example:"2026-01-02T15:04:05Z"`
	NextAttempt *time.Time `json:"nextAttempt,omitempty" example:"2026-03-02T15:04:05Z"`
	CreatedAt   time.Time  `json:"createdAt" example:"2026-01-02T15:04:05Z"`
	UpdatedAt   time.Time  `json:"updatedAt" example:"2026-01-02T15:04:05Z"`
}

func (FrontCert) TableName() string { return "front_certs" }

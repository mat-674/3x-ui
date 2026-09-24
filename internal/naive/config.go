package naive

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

type Client struct {
	Email    string
	Password string
}

type Instance struct {
	Id              int
	Tag             string
	Listen          string
	Port            int
	Domain          string
	CertificateFile string
	KeyFile         string
	CertificatePEM  string
	KeyPEM          string
	FallbackRoot    string
	ProbeResistance bool
	HideIP          bool
	HideVia         bool
	Encode          bool
	Clients         []Client
}

func InstanceFromInbound(ib *model.Inbound) (Instance, bool) {
	if ib == nil || ib.Protocol != model.Naive {
		return Instance{}, false
	}
	var parsed struct {
		Domain          string `json:"domain"`
		CertificateFile string `json:"certificateFile"`
		KeyFile         string `json:"keyFile"`
		TLS             struct {
			CertificateFile string `json:"certificateFile"`
			KeyFile         string `json:"keyFile"`
			Certificates    []struct {
				CertificateFile string   `json:"certificateFile"`
				KeyFile         string   `json:"keyFile"`
				Certificate     []string `json:"certificate"`
				Key             []string `json:"key"`
				Usage           string   `json:"usage"`
			} `json:"certificates"`
		} `json:"tlsSettings"`
		LegacyTLS struct {
			CertificateFile string `json:"certificateFile"`
			KeyFile         string `json:"keyFile"`
		} `json:"tls"`
		FallbackRoot    string `json:"fallbackRoot"`
		ProbeResistance *bool  `json:"probeResistance"`
		HideIP          *bool  `json:"hideIp"`
		HideVia         *bool  `json:"hideVia"`
		Encode          *bool  `json:"encode"`
		Clients         []struct {
			Email      string `json:"email"`
			Password   string `json:"password"`
			Enable     bool   `json:"enable"`
			ExpiryTime int64  `json:"expiryTime"`
		} `json:"clients"`
	}
	if err := json.Unmarshal([]byte(ib.Settings), &parsed); err != nil {
		return Instance{}, false
	}
	certificateFile := strings.TrimSpace(parsed.CertificateFile)
	keyFile := strings.TrimSpace(parsed.KeyFile)
	certificatePEM, keyPEM := "", ""
	if certificateFile == "" {
		certificateFile = strings.TrimSpace(parsed.TLS.CertificateFile)
	}
	if keyFile == "" {
		keyFile = strings.TrimSpace(parsed.TLS.KeyFile)
	}
	if len(parsed.TLS.Certificates) > 0 {
		for _, cert := range parsed.TLS.Certificates {
			if strings.EqualFold(cert.Usage, "verify") {
				continue
			}
			certificateFile = strings.TrimSpace(cert.CertificateFile)
			keyFile = strings.TrimSpace(cert.KeyFile)
			if certificateFile == "" {
				certificatePEM = strings.Join(cert.Certificate, "\n")
			}
			if keyFile == "" {
				keyPEM = strings.Join(cert.Key, "\n")
			}
			break
		}
	}
	if certificateFile == "" {
		certificateFile = strings.TrimSpace(parsed.LegacyTLS.CertificateFile)
	}
	if keyFile == "" {
		keyFile = strings.TrimSpace(parsed.LegacyTLS.KeyFile)
	}
	var stream struct {
		Security string `json:"security"`
		TLS      struct {
			Certificates []struct {
				CertificateFile string   `json:"certificateFile"`
				KeyFile         string   `json:"keyFile"`
				Certificate     []string `json:"certificate"`
				Key             []string `json:"key"`
				Usage           string   `json:"usage"`
			} `json:"certificates"`
		} `json:"tlsSettings"`
	}
	if err := json.Unmarshal([]byte(ib.StreamSettings), &stream); err == nil && strings.EqualFold(stream.Security, "tls") {
		for _, cert := range stream.TLS.Certificates {
			if strings.EqualFold(cert.Usage, "verify") {
				continue
			}
			certificateFile = strings.TrimSpace(cert.CertificateFile)
			keyFile = strings.TrimSpace(cert.KeyFile)
			certificatePEM, keyPEM = "", ""
			if certificateFile == "" {
				certificatePEM = strings.Join(cert.Certificate, "\n")
			}
			if keyFile == "" {
				keyPEM = strings.Join(cert.Key, "\n")
			}
			break
		}
	}
	if strings.TrimSpace(parsed.Domain) == "" || (certificateFile == "" && certificatePEM == "") || (keyFile == "" && keyPEM == "") {
		return Instance{}, false
	}

	clients := make([]Client, 0, len(parsed.Clients))
	now := time.Now().UnixMilli()
	for _, c := range parsed.Clients {
		if !c.Enable || strings.TrimSpace(c.Email) == "" || c.Password == "" || c.ExpiryTime > 0 && c.ExpiryTime <= now {
			continue
		}
		clients = append(clients, Client{
			Email:    strings.TrimSpace(c.Email),
			Password: c.Password,
		})
	}
	if len(clients) == 0 {
		return Instance{}, false
	}

	return Instance{
		Id:              ib.Id,
		Tag:             ib.Tag,
		Listen:          ib.Listen,
		Port:            ib.Port,
		Domain:          strings.TrimSpace(parsed.Domain),
		CertificateFile: certificateFile,
		KeyFile:         keyFile,
		CertificatePEM:  certificatePEM,
		KeyPEM:          keyPEM,
		FallbackRoot:    strings.TrimSpace(parsed.FallbackRoot),
		ProbeResistance: boolValue(parsed.ProbeResistance, true),
		HideIP:          boolValue(parsed.HideIP, true),
		HideVia:         boolValue(parsed.HideVia, true),
		Encode:          boolValue(parsed.Encode, true),
		Clients:         clients,
	}, true
}

func boolValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func (inst Instance) Fingerprint() string {
	clients := append([]Client(nil), inst.Clients...)
	sort.Slice(clients, func(i, j int) bool {
		if clients[i].Email == clients[j].Email {
			return clients[i].Password < clients[j].Password
		}
		return clients[i].Email < clients[j].Email
	})

	h := sha256.New()
	for _, value := range []string{
		inst.Listen,
		strconv.Itoa(inst.Port),
		inst.Tag,
		inst.Domain,
		inst.CertificateFile,
		inst.KeyFile,
		inst.CertificatePEM,
		inst.KeyPEM,
		inst.FallbackRoot,
		strconv.FormatBool(inst.ProbeResistance),
		strconv.FormatBool(inst.HideIP),
		strconv.FormatBool(inst.HideVia),
		strconv.FormatBool(inst.Encode),
	} {
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{0})
	}
	for _, path := range []string{inst.CertificateFile, inst.KeyFile} {
		if path == "" {
			continue
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			_, _ = h.Write([]byte(err.Error()))
		} else {
			_, _ = h.Write(contents)
		}
		_, _ = h.Write([]byte{0})
	}
	for _, client := range clients {
		_, _ = h.Write([]byte(client.Email))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(client.Password))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func GenerateConfig(inst Instance) ([]byte, error) {
	if inst.Port < 1 || inst.Port > 65535 {
		return nil, fmt.Errorf("invalid port %d", inst.Port)
	}
	if !validCaddyHost(inst.Domain) {
		return nil, fmt.Errorf("invalid domain %q", inst.Domain)
	}
	if (strings.TrimSpace(inst.CertificateFile) == "" && strings.TrimSpace(inst.CertificatePEM) == "") || (strings.TrimSpace(inst.KeyFile) == "" && strings.TrimSpace(inst.KeyPEM) == "") {
		return nil, fmt.Errorf("TLS certificate and key are required")
	}
	if strings.TrimSpace(inst.CertificateFile) == "" || strings.TrimSpace(inst.KeyFile) == "" {
		return nil, fmt.Errorf("inline TLS certificate and key must be materialized before generating Caddyfile")
	}
	if len(inst.Clients) == 0 {
		return nil, fmt.Errorf("at least one client is required")
	}

	var b strings.Builder
	b.WriteString("{\n")
	b.WriteString("    auto_https off\n")
	b.WriteString("    order forward_proxy before file_server\n")
	b.WriteString("}\n\n")
	fmt.Fprintf(&b, ":%d {\n", inst.Port)
	if strings.TrimSpace(inst.Listen) != "" {
		fmt.Fprintf(&b, "    bind %s\n", caddyArg(inst.Listen))
	}
	fmt.Fprintf(&b, "    tls %s %s\n", caddyArg(inst.CertificateFile), caddyArg(inst.KeyFile))
	if inst.Encode {
		b.WriteString("    encode gzip\n")
	}
	b.WriteString("    forward_proxy {\n")
	clients := append([]Client(nil), inst.Clients...)
	sort.Slice(clients, func(i, j int) bool { return clients[i].Email < clients[j].Email })
	for _, client := range clients {
		if strings.TrimSpace(client.Email) == "" || strings.Contains(client.Email, ":") || client.Password == "" {
			return nil, fmt.Errorf("client credentials are required")
		}
		fmt.Fprintf(&b, "        basic_auth %s %s\n", caddyArg(client.Email), caddyArg(client.Password))
	}
	if inst.HideIP {
		b.WriteString("        hide_ip\n")
	}
	if inst.HideVia {
		b.WriteString("        hide_via\n")
	}
	if inst.ProbeResistance {
		fmt.Fprintf(&b, "        probe_resistance %s\n", caddyArg(inst.Domain))
	}
	b.WriteString("    }\n")
	if inst.FallbackRoot != "" {
		fmt.Fprintf(&b, "    root * %s\n    file_server\n", caddyArg(inst.FallbackRoot))
	}
	b.WriteString("}\n")
	return []byte(b.String()), nil
}

func validCaddyHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" || strings.ContainsAny(host, "\r\n{}\"' /\\") {
		return false
	}
	bare := strings.Trim(host, "[]")
	if net.ParseIP(bare) != nil {
		return true
	}
	if strings.ContainsAny(host, ":[]") {
		return false
	}
	return strings.Contains(host, ".") || host == "localhost"
}

func caddyHost(host string) string {
	if net.ParseIP(strings.Trim(host, "[]")) != nil && strings.Contains(host, ":") {
		return "[" + strings.Trim(host, "[]") + "]"
	}
	return host
}

func caddyArg(value string) string {
	return strconv.Quote(value)
}

func GetBinaryName() string {
	name := fmt.Sprintf("caddy-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func GetBinaryPath() string {
	custom := filepath.Join(config.GetBinFolderPath(), GetBinaryName())
	if _, err := os.Stat(custom); err == nil {
		return custom
	}
	for _, path := range []string{"/usr/local/bin/caddy", "/usr/bin/caddy"} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	if path, err := exec.LookPath("caddy"); err == nil {
		return path
	}
	return custom
}

func CertificatePathForID(id int) string {
	return filepath.Join(ConfigDir(), fmt.Sprintf("cert_%d.pem", id))
}

func KeyPathForID(id int) string {
	return filepath.Join(ConfigDir(), fmt.Sprintf("key_%d.pem", id))
}

func WriteTLSFiles(id int, certificate, key string) (certPath, keyPath string, err error) {
	if certificate == "" && key == "" {
		return "", "", nil
	}
	if err := os.MkdirAll(ConfigDir(), 0o700); err != nil {
		return "", "", err
	}
	if certificate != "" {
		certPath = CertificatePathForID(id)
		if err := os.WriteFile(certPath, []byte(certificate), 0o600); err != nil {
			return "", "", err
		}
	}
	if key != "" {
		keyPath = KeyPathForID(id)
		if err := os.WriteFile(keyPath, []byte(key), 0o600); err != nil {
			if certPath != "" {
				_ = os.Remove(certPath)
			}
			return "", "", err
		}
	}
	return certPath, keyPath, nil
}

func RemoveTLSFiles(id int) error {
	for _, path := range []string{CertificatePathForID(id), KeyPathForID(id)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func ConfigDir() string {
	return filepath.Join(config.GetBinFolderPath(), "naive")
}

func ConfigPathForID(id int) string {
	return filepath.Join(ConfigDir(), fmt.Sprintf("caddy_%d.Caddyfile", id))
}

func WriteConfigFile(id int, data []byte) (string, error) {
	if err := os.MkdirAll(ConfigDir(), 0o700); err != nil {
		return "", err
	}
	path := ConfigPathForID(id)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func RemoveConfigFile(id int) error {
	if err := os.Remove(ConfigPathForID(id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return RemoveTLSFiles(id)
}

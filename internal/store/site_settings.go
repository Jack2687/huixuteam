package store

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"net"
	"net/mail"
	"regexp"
	"strings"
)

var FeatureNames = []string{"overview", "refunds", "billing", "developer", "announcements", "help", "partners"}

type SiteSecurity struct {
	LoginSliderEnabled       bool     `json:"login_slider_enabled"`
	RegistrationEnabled      bool     `json:"registration_enabled"`
	EmailVerificationEnabled bool     `json:"email_verification_enabled"`
	EmailDomains             []string `json:"email_domains"`
}
type SiteMail struct {
	Host               string `json:"host"`
	Port               int    `json:"port"`
	Username           string `json:"username"`
	FromEmail          string `json:"from_email"`
	FromName           string `json:"from_name"`
	Encryption         string `json:"encryption"`
	PasswordConfigured bool   `json:"password_configured"`
}
type SiteSettings struct {
	PartnerRewardsEnabled bool            `json:"-"`
	Branding              SiteBranding    `json:"branding"`
	CustomMenus           []CustomMenu    `json:"custom_menus"`
	Features              map[string]bool `json:"features"`
	Security              SiteSecurity    `json:"security"`
	Mail                  SiteMail        `json:"mail"`
}
type MailInput struct {
	SiteMail
	Password      string `json:"password"`
	ClearPassword bool   `json:"clear_password"`
}

func (s *Store) SiteSettings(ctx context.Context) (SiteSettings, error) {
	var v SiteSettings
	var features, security, smtp, menus []byte
	var hasPassword bool
	var brandRevision string
	err := s.Pool.QueryRow(ctx, "SELECT features,security,mail,octet_length(mail_password)>0,custom_menus,b.revision,COALESCE((invitations->>'enabled')::boolean,false) FROM site_settings s JOIN site_branding b ON b.id=s.id WHERE s.id").Scan(&features, &security, &smtp, &hasPassword, &menus, &brandRevision, &v.PartnerRewardsEnabled)
	if err != nil {
		return v, err
	}
	v.Branding = brandMetadata(brandRevision)
	if err = json.Unmarshal(features, &v.Features); err != nil {
		return v, err
	}
	if err = json.Unmarshal(security, &v.Security); err != nil {
		return v, err
	}
	if err = json.Unmarshal(smtp, &v.Mail); err != nil {
		return v, err
	}
	v.Mail.PasswordConfigured = hasPassword
	if err = json.Unmarshal(menus, &v.CustomMenus); err != nil {
		return v, err
	}
	if v.CustomMenus == nil {
		v.CustomMenus = []CustomMenu{}
	}
	if v.Security.EmailDomains == nil {
		v.Security.EmailDomains = []string{}
	}
	return v, nil
}
func (v SiteMail) Ready() bool {
	return v.Host != "" && v.Port > 0 && v.FromEmail != "" && (v.Username == "" || v.PasswordConfigured)
}

var emailDomainPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)

func normalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	a, e := mail.ParseAddress(value)
	if e != nil || a.Address != value || len(value) > 254 || strings.ContainsAny(value, "\r\n") {
		return "", invalid("邮箱格式不正确")
	}
	_, domain, ok := strings.Cut(value, "@")
	if !ok || !emailDomainPattern.MatchString(domain) {
		return "", invalid("邮箱域名格式不正确")
	}
	return value, nil
}
func normalizeDomains(values []string) ([]string, error) {
	if len(values) > 100 {
		return nil, invalid("邮箱白名单最多100项")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, v := range values {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" {
			continue
		}
		prefix := "@"
		if strings.HasPrefix(v, "*.") {
			prefix = "*."
			v = strings.TrimPrefix(v, "*.")
		} else {
			v = strings.TrimPrefix(v, "@")
		}
		if len(v) > 253 || !emailDomainPattern.MatchString(v) {
			return nil, invalid("邮箱白名单格式不正确")
		}
		v = prefix + v
		if !seen[v] {
			out = append(out, v)
			seen[v] = true
		}
	}
	return out, nil
}
func (v SiteSecurity) Allows(email string) bool {
	if len(v.EmailDomains) == 0 {
		return true
	}
	_, domain, ok := strings.Cut(email, "@")
	if !ok {
		return false
	}
	for _, value := range v.EmailDomains {
		if value == "@"+domain {
			return true
		}
		if strings.HasPrefix(value, "*.") {
			base := strings.TrimPrefix(value, "*.")
			if domain == base || strings.HasSuffix(domain, "."+base) {
				return true
			}
		}
	}
	return false
}
func (s *Store) SaveSiteFeatures(ctx context.Context, actor string, v map[string]bool) error {
	if len(v) != len(FeatureNames) {
		return invalid("请提交完整的功能开关")
	}
	for _, name := range FeatureNames {
		if _, ok := v[name]; !ok {
			return invalid("功能名称无效")
		}
	}
	return s.saveSitePart(ctx, actor, "features", v)
}
func (s *Store) SaveSiteSecurity(ctx context.Context, actor string, v SiteSecurity) error {
	domains, e := normalizeDomains(v.EmailDomains)
	if e != nil {
		return e
	}
	v.EmailDomains = domains
	return s.saveSitePart(ctx, actor, "security", v)
}
func (s *Store) saveSitePart(ctx context.Context, actor, column string, value any) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = adminLock(ctx, tx, actor); e != nil {
		return e
	}
	var mailJSON []byte
	var password bool
	if e = tx.QueryRow(ctx, "SELECT mail,octet_length(mail_password)>0 FROM site_settings WHERE id FOR UPDATE").Scan(&mailJSON, &password); e != nil {
		return e
	}
	if security, ok := value.(SiteSecurity); ok && security.RegistrationEnabled && security.EmailVerificationEnabled {
		var smtp SiteMail
		if e = json.Unmarshal(mailJSON, &smtp); e != nil {
			return e
		}
		smtp.PasswordConfigured = password
		if !smtp.Ready() {
			return invalid("请先配置邮件服务，再开启邮箱验证注册")
		}
	}
	raw, e := json.Marshal(value)
	if e != nil {
		return e
	}
	// column is an internal constant, never caller input.
	if _, e = tx.Exec(ctx, "UPDATE site_settings SET "+column+"=$1,updated_at=now() WHERE id", raw); e != nil {
		return e
	}
	if e = audit(ctx, tx, actor, "site."+column, "site", value); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) SaveSiteMail(ctx context.Context, actor string, v MailInput) error {
	v.Host = strings.TrimSpace(v.Host)
	v.Username = strings.TrimSpace(v.Username)
	v.FromName = strings.TrimSpace(v.FromName)
	if len(v.Host) > 253 || v.Host == "" || strings.ContainsAny(v.Host, "/\\@ \t\r\n") || (net.ParseIP(v.Host) == nil && !emailDomainPattern.MatchString(v.Host) && v.Host != "localhost") {
		return invalid("SMTP主机无效")
	}
	if v.Port < 1 || v.Port > 65535 {
		return invalid("SMTP端口无效")
	}
	if v.Encryption != "tls" && v.Encryption != "starttls" && v.Encryption != "none" {
		return invalid("邮件加密方式无效")
	}
	if v.Encryption == "none" && v.Username != "" {
		return invalid("SMTP账号认证必须使用TLS或STARTTLS")
	}
	if len(v.Username) > 254 || len(v.Password) > 1024 || len(v.FromName) > 80 || strings.ContainsAny(v.Username+v.FromName, "\r\n") {
		return invalid("邮件字段无效")
	}
	var e error
	v.FromEmail, e = normalizeEmail(v.FromEmail)
	if e != nil {
		return e
	}
	if v.ClearPassword && v.Password != "" {
		return invalid("不能同时清除和设置密码")
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = adminLock(ctx, tx, actor); e != nil {
		return e
	}
	var encrypted []byte
	if e = tx.QueryRow(ctx, "SELECT mail_password FROM site_settings WHERE id FOR UPDATE").Scan(&encrypted); e != nil {
		return e
	}
	if v.ClearPassword {
		encrypted = []byte{}
	}
	if v.Password != "" {
		encrypted, e = s.sealSecret("site-mail", v.Password)
		if e != nil {
			return e
		}
	}
	v.PasswordConfigured = len(encrypted) > 0
	if v.Username != "" && !v.PasswordConfigured {
		return invalid("请填写SMTP授权密码")
	}
	raw, e := json.Marshal(v.SiteMail)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "UPDATE site_settings SET mail=$1,mail_password=$2,updated_at=now() WHERE id", raw, encrypted); e != nil {
		return e
	}
	if e = audit(ctx, tx, actor, "site.mail", "site", map[string]any{"host": v.Host, "port": v.Port, "encryption": v.Encryption, "password_changed": v.Password != "" || v.ClearPassword}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func readSecurityTx(ctx context.Context, tx pgx.Tx) (SiteSecurity, error) {
	var raw []byte
	var v SiteSecurity
	e := tx.QueryRow(ctx, "SELECT security FROM site_settings WHERE id FOR SHARE").Scan(&raw)
	if e == nil {
		e = json.Unmarshal(raw, &v)
	}
	return v, e
}

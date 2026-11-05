package httpapi

import (
	"context"
	"llm-refund/internal/store"
	"net/http"
	"strings"
	"time"
)

type siteMenu struct {
	Key          string `json:"key"`
	Label        string `json:"label"`
	Icon         string `json:"icon"`
	IconURL      string `json:"icon_url,omitempty"`
	OpenInNewTab bool   `json:"open_in_new_tab,omitempty"`
}

var customerMenus = []siteMenu{{Key: "overview", Label: "总览", Icon: "LayoutDashboard"}, {Key: "refunds", Label: "申请记录", Icon: "FileText"}, {Key: "billing", Label: "资金与账单", Icon: "Wallet"}, {Key: "developer", Label: "接口接入", Icon: "CodeXml"}, {Key: "announcements", Label: "公告中心", Icon: "Bell"}, {Key: "help", Label: "使用指南", Icon: "CircleHelp"}}

func enabledMenus(v store.SiteSettings) []siteMenu {
	out := []siteMenu{}
	for _, item := range customerMenus {
		if v.Features[item.Key] {
			out = append(out, item)
		}
	}
	return out
}
func (s *Server) siteRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/public/site", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.DB.SiteSettings(r.Context())
		if e != nil {
			s.result(w, nil, e)
			return
		}
		respond(w, 200, map[string]any{"branding": v.Branding, "menus": enabledMenus(v), "registration_enabled": v.Security.RegistrationEnabled, "email_verification_enabled": v.Security.EmailVerificationEnabled, "email_domains": v.Security.EmailDomains, "password_reset_enabled": v.Mail.Ready()})
	})
	m.Handle("GET /api/workspace/navigation", s.auth(false, func(w http.ResponseWriter, r *http.Request) {
		v, e := s.DB.SiteSettings(r.Context())
		if e != nil {
			s.result(w, nil, e)
			return
		}
		items := enabledMenus(v)
		if current(r).Role == "admin" && current(r).Can("admin.overview.read") {
			items = append(items, siteMenu{Key: "admin", Label: "运营控制台", Icon: "ShieldCheck"})
		}
		items = appendCustomMenus(items, v, current(r))
		respond(w, 200, map[string]any{"menus": items})
	}))
	m.Handle("GET /api/admin/site-settings", s.require("admin.site.manage", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.DB.SiteSettings(r.Context())
		s.result(w, v, e)
	}))
	m.Handle("PUT /api/admin/site-settings/features", s.require("admin.site.manage", func(w http.ResponseWriter, r *http.Request) {
		var v map[string]bool
		if !decode(w, r, &v) {
			return
		}
		s.result(w, map[string]bool{"ok": true}, s.DB.SaveSiteFeatures(r.Context(), current(r).ID, v))
	}))
	m.Handle("PUT /api/admin/site-settings/security", s.require("admin.site.manage", func(w http.ResponseWriter, r *http.Request) {
		var v store.SiteSecurity
		if !decode(w, r, &v) {
			return
		}
		s.result(w, map[string]bool{"ok": true}, s.DB.SaveSiteSecurity(r.Context(), current(r).ID, v))
	}))
	m.Handle("PUT /api/admin/site-settings/mail", s.require("admin.site.manage", func(w http.ResponseWriter, r *http.Request) {
		var v store.MailInput
		if !decode(w, r, &v) {
			return
		}
		s.result(w, map[string]bool{"ok": true}, s.DB.SaveSiteMail(r.Context(), current(r).ID, v))
	}))
	m.Handle("GET /api/admin/site-settings/mail/logs", s.require("admin.site.manage", func(w http.ResponseWriter, r *http.Request) {
		p, size := pagination(r)
		items, total, e := s.DB.MailLogs(r.Context(), p, size)
		s.result(w, map[string]any{"items": items, "total": total, "page": p, "size": size}, e)
	}))
	m.Handle("POST /api/admin/site-settings/mail/test", s.require("admin.site.manage", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Email string `json:"email"`
		}
		if !decode(w, r, &v) {
			return
		}
		s.result(w, map[string]bool{"queued": true}, s.DB.QueueTestMail(r.Context(), current(r).ID, v.Email))
	}))
	m.Handle("POST /api/admin/site-settings/mail/connection", s.require("admin.site.manage", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		s.result(w, map[string]bool{"ok": true}, s.DB.MailConnectionTest(ctx, current(r).ID))
	}))
	m.HandleFunc("POST /api/auth/email-code", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Email   string `json:"email"`
			Purpose string `json:"purpose"`
		}
		if !decode(w, r, &v) {
			return
		}
		if !s.emailRate(w, r, v.Email, "send") {
			return
		}
		if e := s.DB.RequestEmailCode(r.Context(), v.Email, v.Purpose); e != nil {
			s.result(w, nil, e)
			return
		}
		respond(w, 202, map[string]any{"message": "如该邮箱符合条件，验证码邮件将发送，请查看收件箱。", "retry_after": 60})
	})
	m.HandleFunc("POST /api/auth/register", func(w http.ResponseWriter, r *http.Request) {
		var v store.RegistrationInput
		if !decode(w, r, &v) {
			return
		}
		if !s.emailRate(w, r, v.Email, "submit") {
			return
		}
		if e := s.DB.Register(r.Context(), v); e != nil {
			s.result(w, nil, e)
			return
		}
		respond(w, 201, map[string]bool{"ok": true})
	})
	m.HandleFunc("POST /api/auth/reset-password", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Email    string `json:"email"`
			Code     string `json:"code"`
			Password string `json:"password"`
		}
		if !decode(w, r, &v) {
			return
		}
		if !s.emailRate(w, r, v.Email, "submit") {
			return
		}
		s.result(w, map[string]bool{"ok": true}, s.DB.ResetPasswordByEmail(r.Context(), v.Email, v.Code, v.Password))
	})
}
func (s *Server) emailRate(w http.ResponseWriter, r *http.Request, email, operation string) bool {
	if len(email) > 254 {
		fail(w, 400, "邮箱格式不正确")
		return false
	}
	meta := riskMeta(r)
	if meta == nil {
		fail(w, 503, "服务暂时不可用")
		return false
	}
	e := s.DB.AuthRate(r.Context(), meta.Observation.SourceIP, email, operation)
	if e != nil {
		s.result(w, nil, e)
		return false
	}
	return true
}

// This gate applies to APIs, direct URLs, and lazy JS/CSS modules. A disabled
// feature cannot be recovered by editing the DOM or replaying a captured call.
func featureForPath(path string) []string {
	switch {
	case path == "/api/dashboard":
		return []string{"overview"}
	case path == "/api/refunds" || strings.HasPrefix(path, "/api/refunds/"):
		return []string{"refunds"}
	case strings.HasPrefix(path, "/open/v1/"):
		return []string{"developer", "refunds"}
	case path == "/api/keys" || strings.HasPrefix(path, "/api/keys/") || path == "/developers/reference":
		return []string{"developer"}
	case path == "/api/billing" || path == "/api/redeem":
		return []string{"billing"}
	case path == "/api/announcements" || path == "/api/public/announcements":
		return []string{"announcements"}
	}
	for _, name := range store.FeatureNames {
		if strings.HasPrefix(path, "/assets/feature-"+name+"-") || path == "/workspace/"+name {
			return []string{name}
		}
	}
	return nil
}
func (s *Server) featureGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		features := featureForPath(r.URL.Path)
		if len(features) > 0 {
			v, e := s.DB.SiteSettings(r.Context())
			if e != nil {
				fail(w, 503, "功能配置暂不可用")
				return
			}
			for _, name := range features {
				if !v.Features[name] {
					fail(w, 404, "该功能暂未开放")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

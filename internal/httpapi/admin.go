package httpapi

import (
	"llm-refund/internal/store"
	"net/http"
	"time"
)

func (s *Server) require(permission string, next http.HandlerFunc) http.Handler {
	return s.auth(false, func(w http.ResponseWriter, r *http.Request) {
		if !current(r).Can(permission) {
			fail(w, 403, "没有访问此管理功能的权限")
			return
		}
		next(w, r)
	})
}
func (s *Server) adminRoutes(m *http.ServeMux) {
	m.Handle("GET /api/admin/overview", s.require("admin.overview.read", func(w http.ResponseWriter, r *http.Request) {
		data, err := s.DB.AdminOverview(r.Context(), current(r).Role == "admin")
		s.result(w, data, err)
	}))
	m.Handle("GET /api/admin/users", s.require("admin.users.manage", func(w http.ResponseWriter, r *http.Request) {
		p, size := pagination(r)
		items, total, err := s.DB.AdminUsers(r.Context(), r.URL.Query().Get("q"), p, size)
		s.result(w, map[string]any{"items": items, "total": total}, err)
	}))
	m.Handle("POST /api/admin/users", s.require("admin.users.manage", func(w http.ResponseWriter, r *http.Request) {
		var body store.UserInput
		if !decode(w, r, &body) {
			return
		}
		id, err := s.DB.AdminCreateUser(r.Context(), current(r).ID, body)
		s.result(w, map[string]string{"id": id}, err)
	}))
	m.Handle("PATCH /api/admin/users/{id}", s.require("admin.users.manage", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name       string `json:"name"`
			Role       string `json:"role"`
			ServiceFee int64  `json:"service_fee"`
			Disabled   bool   `json:"disabled"`
		}
		if !decode(w, r, &body) {
			return
		}
		s.result(w, map[string]bool{"ok": true}, s.DB.AdminUpdateUser(r.Context(), current(r).ID, r.PathValue("id"), store.UserInput{Name: body.Name, Role: body.Role, ServiceFee: body.ServiceFee, Disabled: body.Disabled}))
	}))
	m.Handle("POST /api/admin/users/{id}/password", s.require("admin.users.manage", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Password string `json:"password"`
		}
		if !decode(w, r, &body) {
			return
		}
		s.result(w, map[string]bool{"ok": true}, s.DB.AdminPassword(r.Context(), current(r).ID, r.PathValue("id"), body.Password))
	}))
	m.Handle("POST /api/admin/users/{id}/credit", s.require("admin.credits.write", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Amount int64  `json:"amount"`
			Note   string `json:"note"`
		}
		if !decode(w, r, &body) {
			return
		}
		s.result(w, map[string]bool{"ok": true}, s.DB.AdminCredit(r.Context(), current(r).ID, r.PathValue("id"), r.Header.Get("Idempotency-Key"), body.Amount, body.Note))
	}))
	m.Handle("GET /api/admin/refunds", s.require("admin.refunds.read", func(w http.ResponseWriter, r *http.Request) {
		p, size := pagination(r)
		items, total, err := s.DB.AdminRefunds(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get("status"), p, size)
		s.result(w, map[string]any{"items": items, "total": total}, err)
	}))
	m.Handle("GET /api/admin/refunds/{id}", s.require("admin.refunds.read", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		owner, err := s.DB.RefundOwner(r.Context(), id)
		if err != nil {
			s.result(w, nil, err)
			return
		}
		refund, err := s.DB.Refund(r.Context(), owner, id)
		if err != nil {
			s.result(w, nil, err)
			return
		}
		events, err := s.DB.Events(r.Context(), owner, id)
		if err != nil {
			s.result(w, nil, err)
			return
		}
		result := map[string]any{"refund": refund, "events": events, "provider_ready": s.DB.ProviderReady(r.Context())}
		if current(r).Can("admin.refund.execute") {
			e, err := s.DB.RefundExecution(r.Context(), id)
			if err != nil {
				s.result(w, nil, err)
				return
			}
			if e != nil && refund.Status == "attention" {
				result["recovery"] = map[string]bool{"running": e.Running, "needs_review": e.Step == "submitting", "can_release": e.AcceptedAt == nil && e.Step != "submitting"}
			}
		}
		s.result(w, result, nil)
	}))
	m.Handle("GET /api/admin/audit", s.require("admin.audit.read", func(w http.ResponseWriter, r *http.Request) {
		p, size := pagination(r)
		items, total, err := s.DB.Audits(r.Context(), p, size)
		s.result(w, map[string]any{"items": items, "total": total}, err)
	}))
	m.Handle("GET /api/admin/codes", s.require("admin.codes.manage", func(w http.ResponseWriter, r *http.Request) {
		p, size := pagination(r)
		items, total, err := s.DB.Codes(r.Context(), p, size)
		s.result(w, map[string]any{"items": items, "total": total}, err)
	}))
	m.Handle("POST /api/admin/codes", s.require("admin.codes.manage", func(w http.ResponseWriter, r *http.Request) {
		var body store.CodeInput
		if !decode(w, r, &body) {
			return
		}
		codes, err := s.DB.GenerateCodes(r.Context(), current(r).ID, body)
		s.result(w, map[string]any{"codes": codes}, err)
	}))
	m.Handle("PATCH /api/admin/codes/{id}", s.require("admin.codes.manage", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Disabled bool `json:"disabled"`
		}
		if !decode(w, r, &body) {
			return
		}
		s.result(w, map[string]bool{"ok": true}, s.DB.DisableCode(r.Context(), current(r).ID, r.PathValue("id"), body.Disabled))
	}))
	m.Handle("POST /api/admin/codes/export", s.require("admin.codes.manage", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IDs []string `json:"ids"`
		}
		if !decode(w, r, &body) {
			return
		}
		codes, err := s.DB.ExportCodes(r.Context(), current(r).ID, body.IDs)
		s.result(w, map[string]any{"codes": codes}, err)
	}))
	m.Handle("POST /api/admin/codes/delete", s.require("admin.codes.manage", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IDs []string `json:"ids"`
		}
		if !decode(w, r, &body) {
			return
		}
		s.result(w, map[string]bool{"ok": true}, s.DB.DeleteCodes(r.Context(), current(r).ID, body.IDs))
	}))
	m.Handle("GET /api/admin/redemptions", s.require("admin.codes.manage", func(w http.ResponseWriter, r *http.Request) {
		p, size := pagination(r)
		items, total, err := s.DB.Redemptions(r.Context(), p, size)
		s.result(w, map[string]any{"items": items, "total": total}, err)
	}))
	m.Handle("POST /api/redeem", s.require("codes.redeem", s.redeem))
}
func (s *Server) redeem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &body) {
		return
	}
	key := "redeem:" + current(r).ID
	s.mu.Lock()
	now := time.Now()
	a := s.attempts[key]
	if now.After(a.until) {
		a = attempt{until: now.Add(time.Minute)}
	}
	if a.count >= 20 {
		s.mu.Unlock()
		w.Header().Set("Retry-After", "60")
		fail(w, 429, "兑换尝试过于频繁，请稍后重试")
		return
	}
	a.count++
	s.attempts[key] = a
	s.mu.Unlock()
	amount, err := s.DB.Redeem(r.Context(), current(r).ID, body.Code)
	s.result(w, map[string]any{"amount": amount, "message": "兑换成功，积分已入账"}, err)
}

package httpapi

import (
	"llm-refund/internal/store"
	"net/http"
)

func (s *Server) partnerRoutes(m *http.ServeMux) {
	m.Handle("GET /api/partners", s.auth(false, func(w http.ResponseWriter, r *http.Request) {
		p, n := pagination(r)
		v, e := s.DB.Partners(r.Context(), current(r).ID, "", p, n)
		s.result(w, v, e)
	}))
	m.Handle("GET /api/admin/invites", s.require("admin.site.manage", func(w http.ResponseWriter, r *http.Request) {
		p, n := pagination(r)
		v, e := s.DB.Partners(r.Context(), "", r.URL.Query().Get("q"), p, n)
		s.result(w, v, e)
	}))
	m.Handle("PUT /api/admin/invites", s.require("admin.site.manage", func(w http.ResponseWriter, r *http.Request) {
		var v store.PartnerRules
		if !decode(w, r, &v) {
			return
		}
		s.result(w, map[string]bool{"ok": true}, s.DB.SavePartnerRules(r.Context(), current(r).ID, v))
	}))
}

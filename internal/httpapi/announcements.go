package httpapi

import (
	"llm-refund/internal/store"
	"net/http"
)

func (s *Server) announcementRoutes(m *http.ServeMux) {
	list := func(drafts bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			p, size := pagination(r)
			items, total, err := s.DB.Announcements(r.Context(), drafts, p, size)
			s.result(w, map[string]any{"items": items, "total": total, "page": p, "size": size}, err)
		}
	}
	m.Handle("GET /api/announcements", s.auth(false, list(false)))
	// Login page feed has no authenticated or draft scope, even with query flags.
	m.HandleFunc("GET /api/public/announcements", list(false))
	m.Handle("GET /api/admin/announcements", s.require("admin.announcements.manage", list(true)))
	save := func(w http.ResponseWriter, r *http.Request) {
		var in store.AnnouncementInput
		if !decode(w, r, &in) {
			return
		}
		id, err := s.DB.SaveAnnouncement(r.Context(), current(r).ID, r.PathValue("id"), in)
		s.result(w, map[string]string{"id": id}, err)
	}
	m.Handle("POST /api/admin/announcements", s.require("admin.announcements.manage", save))
	m.Handle("PUT /api/admin/announcements/{id}", s.require("admin.announcements.manage", save))
	m.Handle("DELETE /api/admin/announcements/{id}", s.require("admin.announcements.manage", func(w http.ResponseWriter, r *http.Request) {
		s.result(w, map[string]bool{"ok": true}, s.DB.DeleteAnnouncement(r.Context(), current(r).ID, r.PathValue("id")))
	}))
}

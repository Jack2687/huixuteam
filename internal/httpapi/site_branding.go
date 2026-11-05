package httpapi

import (
	"io"
	"llm-refund/internal/store"
	"net/http"
)

func (s *Server) brandingRoutes(m *http.ServeMux) {
	serve := func(w http.ResponseWriter, r *http.Request, data []byte, mime string) {
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		if r.Method != http.MethodHead {
			w.Write(data)
		}
	}
	m.HandleFunc("GET /brand-default.svg", func(w http.ResponseWriter, r *http.Request) {
		serve(w, r, store.DefaultBrandIcon, "image/svg+xml")
	})
	m.HandleFunc("GET /api/public/brand-icon", func(w http.ResponseWriter, r *http.Request) {
		data, mime, err := s.DB.BrandIcon(r.Context())
		if err != nil {
			s.result(w, nil, err)
			return
		}
		serve(w, r, data, mime)
	})
	m.Handle("GET /api/admin/site-settings/branding", s.require("admin.site.manage", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.DB.SiteBranding(r.Context())
		s.result(w, v, err)
	}))
	m.Handle("PUT /api/admin/site-settings/branding", s.require("admin.site.manage", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, store.MaxBrandIconBytes)
		data, err := io.ReadAll(r.Body)
		if err != nil {
			fail(w, 413, "图标文件不能超过1MB")
			return
		}
		v, err := s.DB.SaveBrandIcon(r.Context(), current(r).ID, data, false)
		s.result(w, v, err)
	}))
	m.Handle("DELETE /api/admin/site-settings/branding", s.require("admin.site.manage", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.DB.SaveBrandIcon(r.Context(), current(r).ID, nil, true)
		s.result(w, v, err)
	}))
}

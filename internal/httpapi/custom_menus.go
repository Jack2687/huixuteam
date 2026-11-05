package httpapi

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"llm-refund/internal/store"
	"llm-refund/webui"
	"net/http"
)

func appendCustomMenus(items []siteMenu, settings store.SiteSettings, user store.User) []siteMenu {
	for _, menu := range settings.CustomMenus {
		if !menu.VisibleTo(user) {
			continue
		}
		item := siteMenu{Key: "custom:" + menu.ID, Label: menu.Label, Icon: "ExternalLink", OpenInNewTab: menu.OpenInNewTab}
		if menu.IconSVG != "" {
			item.IconURL = fmt.Sprintf("/api/workspace/custom-menus/%s/icon?v=%x", menu.ID, sha256.Sum256([]byte(menu.IconSVG)))
		}
		items = append(items, item)
	}
	return items
}

func (s *Server) customMenuRoutes(m *http.ServeMux) {
	m.Handle("PUT /api/admin/site-settings/custom-menus", s.require("admin.site.manage", func(w http.ResponseWriter, r *http.Request) {
		var items []store.CustomMenu
		if !decode(w, r, &items) {
			return
		}
		s.result(w, map[string]bool{"ok": true}, s.DB.SaveCustomMenus(r.Context(), current(r).ID, items))
	}))
	withMenu := func(next func(http.ResponseWriter, *http.Request, store.CustomMenu)) http.Handler {
		return s.auth(false, func(w http.ResponseWriter, r *http.Request) {
			item, err := s.DB.CustomMenu(r.Context(), current(r), r.PathValue("id"))
			if err != nil {
				s.result(w, nil, err)
				return
			}
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Set("Vary", "Cookie")
			next(w, r, item)
		})
	}
	m.Handle("GET /api/workspace/custom-menus/{id}", withMenu(func(w http.ResponseWriter, r *http.Request, item store.CustomMenu) { respond(w, 200, item) }))
	m.Handle("GET /api/workspace/custom-menus/{id}/icon", withMenu(func(w http.ResponseWriter, r *http.Request, item store.CustomMenu) {
		if item.IconSVG == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		w.Write([]byte(item.IconSVG))
	}))
	m.Handle("GET /workspace/custom/{id}/open", withMenu(func(w http.ResponseWriter, r *http.Request, item store.CustomMenu) {
		w.Header().Set("Referrer-Policy", "no-referrer")
		http.Redirect(w, r, item.URL, http.StatusFound)
	}))
	m.Handle("GET /workspace/custom/{id}", withMenu(func(w http.ResponseWriter, r *http.Request, item store.CustomMenu) {
		body, err := fs.ReadFile(webui.Static, "static/index.html")
		if err != nil {
			fail(w, 503, "前端资源不可用")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(body)
	}))
}

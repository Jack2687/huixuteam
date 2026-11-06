package httpapi

import (
	"llm-refund/internal/store"
	"net/http"
)

func sliderBinding(r *http.Request) string {
	c, e := r.Cookie("login_slider")
	if e != nil || len(c.Value) > 128 {
		return ""
	}
	return c.Value
}
func (s *Server) sliderRoutes(m *http.ServeMux) {
	limit := func(w http.ResponseWriter, r *http.Request) bool {
		meta := riskMeta(r)
		if meta == nil {
			fail(w, 503, "服务暂不可用")
			return false
		}
		if e := s.DB.SliderLimit(r.Context(), meta.Observation.SourceIP); e != nil {
			s.result(w, nil, e)
			return false
		}
		return true
	}
	m.HandleFunc("GET /api/auth/slider", func(w http.ResponseWriter, r *http.Request) {
		if !limit(w, r) {
			return
		}
		binding := sliderBinding(r)
		if binding == "" {
			binding = store.Token()
		}
		v, e := s.DB.LoginSlider(r.Context(), binding)
		if e == nil {
			http.SetCookie(w, &http.Cookie{Name: "login_slider", Value: binding, Path: "/", HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteStrictMode, MaxAge: 600})
		}
		s.result(w, v, e)
	})
	m.HandleFunc("POST /api/auth/slider/verify", func(w http.ResponseWriter, r *http.Request) {
		if !limit(w, r) {
			return
		}
		var v struct {
			ID string  `json:"id"`
			X  float64 `json:"x"`
		}
		if !decode(w, r, &v) {
			return
		}
		result, e := s.DB.VerifyLoginSlider(r.Context(), sliderBinding(r), v.ID, v.X)
		s.result(w, result, e)
	})
}

package httpapi

import (
	"context"
	"llm-refund/internal/refundworker"
	"llm-refund/internal/store"
	"net/http"
	"time"
)

func (s *Server) executionRoutes(m *http.ServeMux) {
	require := func(h http.HandlerFunc) http.Handler { return s.require("admin.refund.execute", h) }
	m.Handle("GET /api/admin/refund-settings", require(func(w http.ResponseWriter, r *http.Request) {
		cfg, err := s.DB.RefundSettings(r.Context())
		if err != nil {
			s.result(w, nil, err)
			return
		}
		mothers, err := s.DB.RefundMothers(r.Context())
		if err != nil {
			s.result(w, nil, err)
			return
		}
		proxies, err := s.DB.RefundProxies(r.Context())
		s.result(w, map[string]any{"settings": cfg, "mothers": mothers, "proxies": proxies, "provider_ready": s.DB.ProviderReady(r.Context())}, err)
	}))
	m.Handle("PUT /api/admin/refund-settings", require(func(w http.ResponseWriter, r *http.Request) {
		v, err := s.DB.RefundSettings(r.Context())
		if err != nil {
			s.result(w, nil, err)
			return
		}
		if !decode(w, r, &v) {
			return
		}
		if v.Enabled && !s.DB.RefundRuntimeAvailable() {
			fail(w, 400, "服务运行环境尚未就绪，请联系服务器维护人员")
			return
		}
		s.result(w, map[string]bool{"ok": true}, s.DB.SaveRefundSettings(r.Context(), current(r).ID, v))
	}))
	m.Handle("POST /api/admin/refund-mothers/preview", require(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Session string `json:"session"`
		}
		if !decode(w, r, &v) {
			return
		}
		preview, err := store.PreviewRefundMother(v.Session)
		s.result(w, preview, err)
	}))
	m.Handle("POST /api/admin/refund-mothers/check", require(func(w http.ResponseWriter, r *http.Request) {
		var v store.RefundMother
		if !decode(w, r, &v) {
			return
		}
		started := time.Now().UTC()
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		job, err := s.DB.PrepareMotherProbe(ctx, current(r).ID, v)
		if err != nil {
			s.result(w, nil, err)
			return
		}
		result, err := refundworker.DefaultRunner().ProbeMother(ctx, job)
		if err == nil && v.ID != "" && v.Name == "" && v.Email == "" && v.TeamID == "" && v.ProxyID == "" && v.AccessToken == "" && v.Session == "" && v.RefreshToken == "" {
			err = s.DB.SaveResourceCheck(r.Context(), current(r).ID, "mother", v.ID, started, result)
		}
		s.result(w, result, err)
	}))
	m.Handle("POST /api/admin/refund-proxies/check", require(func(w http.ResponseWriter, r *http.Request) {
		var v store.RefundProxy
		if !decode(w, r, &v) {
			return
		}
		started := time.Now().UTC()
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		proxy, err := s.DB.PrepareProxyProbe(ctx, current(r).ID, v)
		if err != nil {
			s.result(w, nil, err)
			return
		}
		result, err := refundworker.DefaultRunner().ProbeProxy(ctx, proxy)
		if err == nil && v.ID != "" && v.URL == "" && v.Name == "" {
			err = s.DB.SaveResourceCheck(r.Context(), current(r).ID, "proxy", v.ID, started, result)
		}
		s.result(w, result, err)
	}))
	m.Handle("POST /api/admin/refund-mothers", require(func(w http.ResponseWriter, r *http.Request) {
		var v store.RefundMother
		if !decode(w, r, &v) {
			return
		}
		id, err := s.DB.SaveRefundMother(r.Context(), current(r).ID, v)
		s.result(w, map[string]string{"id": id}, err)
	}))
	m.Handle("POST /api/admin/refund-proxies", require(func(w http.ResponseWriter, r *http.Request) {
		var v store.RefundProxy
		if !decode(w, r, &v) {
			return
		}
		id, err := s.DB.SaveRefundProxy(r.Context(), current(r).ID, v)
		s.result(w, map[string]string{"id": id}, err)
	}))
	for _, kind := range []string{"mothers", "proxies"} {
		resource := kind
		m.Handle("DELETE /api/admin/refund-"+resource+"/{id}", require(func(w http.ResponseWriter, r *http.Request) {
			name := "mother"
			if resource == "proxies" {
				name = "proxy"
			}
			s.result(w, map[string]bool{"ok": true}, s.DB.DeleteRefundResource(r.Context(), current(r).ID, name, r.PathValue("id")))
		}))
	}
	m.Handle("POST /api/admin/refunds/{id}/resume", require(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Action string `json:"action"`
			Note   string `json:"note"`
		}
		if !decode(w, r, &v) {
			return
		}
		s.result(w, map[string]bool{"ok": true}, s.DB.ResumeRefund(r.Context(), current(r).ID, r.PathValue("id"), v.Action, v.Note))
	}))
}

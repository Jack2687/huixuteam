package httpapi

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"llm-refund/internal/store"
)

type riskContextKey struct{}
type riskRequest struct {
	Config      store.RiskSettings
	Observation store.RiskObservation
	Kind        string
}
type riskResponse struct {
	http.ResponseWriter
	status int
}

func (w *riskResponse) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
		w.ResponseWriter.WriteHeader(code)
	}
}
func (w *riskResponse) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}
func (w *riskResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func riskMeta(r *http.Request) *riskRequest {
	v, _ := r.Context().Value(riskContextKey{}).(*riskRequest)
	return v
}

func riskOperation(r *http.Request) string {
	p := r.URL.Path
	switch {
	case p == "/api/auth/login":
		return "login"
	case p == "/api/auth/logout":
		return "logout"
	case strings.HasPrefix(p, "/api/admin/risk-"):
		return "risk"
	case strings.HasPrefix(p, "/api/admin/refund-"):
		return "resources"
	case strings.HasPrefix(p, "/api/admin/"):
		return "admin"
	case p == "/api/redeem":
		return "redeem"
	case strings.HasPrefix(p, "/api/keys"):
		return "keys"
	case (p == "/api/refunds" || p == "/open/v1/requests") && r.Method == "POST":
		return "submit"
	case (strings.HasPrefix(p, "/api/refunds/") && strings.HasSuffix(p, "/cancel")) || (strings.HasPrefix(p, "/open/v1/requests/") && strings.HasSuffix(p, "/withdraw")):
		return "cancel"
	case r.Method == "GET":
		return "query"
	default:
		return "unknown"
	}
}

// Trust only explicitly configured proxy hops; walk XFF from the socket toward
// the client. Client-supplied CF/X-Real-IP headers alone never establish identity.
func riskSource(r *http.Request, trusted []string) (string, bool) {
	peer := r.RemoteAddr
	if host, _, err := net.SplitHostPort(peer); err == nil {
		peer = host
	}
	addr, err := netip.ParseAddr(peer)
	if err != nil {
		return "", false
	}
	addr = addr.Unmap()
	isTrusted := func(ip netip.Addr) bool {
		for _, v := range trusted {
			if p, e := netip.ParsePrefix(v); e == nil && p.Contains(ip) {
				return true
			}
			if a, e := netip.ParseAddr(v); e == nil && a.Unmap() == ip {
				return true
			}
		}
		return false
	}
	chain := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	if !isTrusted(addr) {
		return addr.String(), len(trusted) > 0 && !addr.IsPrivate() && !addr.IsLoopback()
	}
	if len(chain) > 32 {
		return addr.String(), false
	}
	for i := len(chain) - 1; i >= 0; i-- {
		next, e := netip.ParseAddr(strings.TrimSpace(chain[i]))
		if e != nil {
			return addr.String(), false
		}
		addr = next.Unmap()
		if !isTrusted(addr) {
			return addr.String(), true
		}
	}
	return addr.String(), false
}

func (s *Server) riskMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !(strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/open/")) {
			next.ServeHTTP(w, r)
			return
		}
		cfg, err := s.DB.RiskSettings(r.Context())
		if err != nil {
			s.result(w, nil, err)
			return
		}
		ip, verified := riskSource(r, cfg.TrustedProxies)
		channel := "web"
		if strings.HasPrefix(r.URL.Path, "/open/") {
			channel = "api"
		}
		meta := &riskRequest{Config: cfg, Observation: store.RiskObservation{SourceIP: ip, SourceVerified: verified, Operation: riskOperation(r), Channel: channel}}
		r = r.WithContext(context.WithValue(r.Context(), riskContextKey{}, meta))
		started := time.Now()
		rw := &riskResponse{ResponseWriter: w}
		defer func() {
			// Do not make the monitor's own polling inflate traffic statistics.
			if !cfg.Enabled || (meta.Observation.Operation == "risk" && r.Method == "GET") {
				return
			}
			if rw.status == 0 {
				rw.status = 200
			}
			meta.Observation.Status = rw.status
			meta.Observation.DurationMS = time.Since(started).Milliseconds()
			if meta.Kind == "" {
				switch {
				case rw.status >= 500:
					meta.Kind = "server_error"
				case rw.status == 403:
					meta.Kind = "denied"
				case rw.status == 401:
					meta.Kind = "auth_failed"
				case rw.status == 409 && meta.Observation.Operation == "submit":
					meta.Kind = "duplicate"
				case rw.status >= 400 && meta.Observation.Operation == "redeem":
					meta.Kind = "redeem_failed"
				case rw.status < 300 && meta.Observation.Operation == "cancel":
					meta.Kind = "cancelled"
				}
			}
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
			defer cancel()
			if err := s.DB.RecordRiskRequest(ctx, meta.Observation, meta.Kind); err != nil {
				slog.Error("risk telemetry unavailable")
			}
		}()
		next.ServeHTTP(rw, r)
	})
}

func (s *Server) applyRiskChecks(w http.ResponseWriter, r *http.Request, checks []store.RiskConsumption) bool {
	decisions, err := s.DB.ConsumeRisk(r.Context(), checks)
	if err != nil {
		s.result(w, nil, err)
		return false
	}
	meta := riskMeta(r)
	for _, d := range decisions {
		if d.Exceeded && meta != nil {
			meta.Kind = "rate_observed"
			meta.Observation.Rule = d.Rule
		}
		if d.Blocked {
			if meta != nil {
				meta.Kind = "rate_blocked"
				meta.Observation.Rule = d.Rule
			}
			w.Header().Set("Retry-After", strconv.Itoa(d.RetrySeconds))
			message := "访问过于频繁，请 " + strconv.Itoa(d.RetrySeconds) + " 秒后重试"
			if d.Rule == "login" {
				message = "此账号登录尝试过多，请 " + strconv.Itoa((d.RetrySeconds+59)/60) + " 分钟后重试"
			}
			fail(w, 429, message)
			return false
		}
	}
	return true
}

func (s *Server) checkAuthenticatedRisk(w http.ResponseWriter, r *http.Request, u store.User, token string, isKey bool) bool {
	meta := riskMeta(r)
	if meta == nil {
		return true
	}
	meta.Observation.UserID = u.ID
	meta.Observation.Account = u.Email
	var key store.RiskIdentity
	if isKey {
		var err error
		key, err = s.DB.RiskKeyIdentity(r.Context(), u.ID, token)
		if err != nil {
			fail(w, 401, "登录已过期或密钥已失效")
			return false
		}
		meta.Observation.KeyID = key.ID
	}
	// Authorized risk controls and logout remain reachable even when a customer's
	// query bucket is limited. No bypass is granted before authentication.
	if ((meta.Observation.Operation == "risk" || r.URL.Path == "/api/auth/me") && u.Can("admin.risk.manage")) || meta.Observation.Operation == "logout" {
		return true
	}
	control, err := s.DB.RiskUserControl(r.Context(), u.ID)
	if err != nil {
		s.result(w, nil, err)
		return false
	}
	identity := store.RiskIdentity{Type: "user", ID: u.ID, Label: u.Email, UserID: u.ID}
	names := []string{"account"}
	switch {
	case meta.Observation.Operation == "submit":
		names = append(names, "submit")
	case meta.Observation.Operation == "redeem":
		names = append(names, "redeem")
	case r.Method == "GET":
		names = append(names, "query")
	default:
		names = append(names, "write")
	}
	if isKey {
		names = append(names, "api_key")
	}
	checks := []store.RiskConsumption{}
	for _, name := range names {
		rule := meta.Config.Rules[name]
		if value := control.Limits[name]; value > 0 {
			rule.Limit = value
		}
		who := identity
		if name == "api_key" {
			who = key
		}
		checks = append(checks, store.RiskConsumption{Rule: name, Policy: rule, Identity: who})
	}
	return s.applyRiskChecks(w, r, checks)
}

func safeLoginAccount(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	a, err := mail.ParseAddress(email)
	if err != nil || a.Address != email || len(email) > 254 {
		return "格式无效的账号"
	}
	return email
}

func (s *Server) riskRoutes(m *http.ServeMux) {
	require := func(h http.HandlerFunc) http.Handler { return s.require("admin.risk.manage", h) }
	m.Handle("GET /api/admin/risk-settings", require(func(w http.ResponseWriter, r *http.Request) {
		cfg, err := s.DB.RiskSettings(r.Context())
		s.result(w, map[string]any{"settings": cfg, "defaults": store.DefaultRiskSettings(), "rule_names": store.RiskRuleNames}, err)
	}))
	m.Handle("PUT /api/admin/risk-settings", require(func(w http.ResponseWriter, r *http.Request) {
		var cfg store.RiskSettings
		if !decode(w, r, &cfg) {
			return
		}
		s.result(w, map[string]bool{"ok": true}, s.DB.SaveRiskSettings(r.Context(), current(r).ID, cfg))
	}))
	m.Handle("GET /api/admin/risk-monitor", require(func(w http.ResponseWriter, r *http.Request) {
		p, size := pagination(r)
		q := r.URL.Query()
		if q.Get("size") == "" {
			size = 100
		}
		out, err := s.DB.RiskMonitor(r.Context(), store.RiskMonitorFilter{Page: p, Size: size, Query: q.Get("q"), Start: q.Get("start"), End: q.Get("end"), Category: q.Get("category"), State: q.Get("state")})
		s.result(w, out, err)
	}))
	m.Handle("POST /api/admin/risk-events/{id}/resolve", require(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id < 1 {
			fail(w, 400, "事件编号无效")
			return
		}
		s.result(w, map[string]bool{"ok": true}, s.DB.ResolveRiskEvent(r.Context(), current(r).ID, id))
	}))
	m.Handle("POST /api/admin/risk-limits/{id}/release", require(func(w http.ResponseWriter, r *http.Request) {
		s.result(w, map[string]bool{"ok": true}, s.DB.ReleaseRiskLimit(r.Context(), current(r).ID, r.PathValue("id")))
	}))
	m.Handle("GET /api/admin/risk-customers", require(func(w http.ResponseWriter, r *http.Request) {
		p, size := pagination(r)
		q := r.URL.Query().Get("q")
		if len(q) > 254 {
			fail(w, 400, "搜索内容过长")
			return
		}
		users, total, err := s.DB.AdminUsers(r.Context(), q, p, size)
		s.result(w, map[string]any{"items": users, "total": total}, err)
	}))
	m.Handle("GET /api/admin/risk-customers/{id}", require(func(w http.ResponseWriter, r *http.Request) {
		control, err := s.DB.RiskUserControl(r.Context(), r.PathValue("id"))
		s.result(w, control, err)
	}))
	m.Handle("PUT /api/admin/risk-customers/{id}", require(func(w http.ResponseWriter, r *http.Request) {
		var v store.RiskUserControl
		if !decode(w, r, &v) {
			return
		}
		v.UserID = r.PathValue("id")
		s.result(w, map[string]bool{"ok": true}, s.DB.SaveRiskUserControl(r.Context(), current(r).ID, v))
	}))
	m.Handle("POST /api/admin/risk-keys/{id}/revoke", require(func(w http.ResponseWriter, r *http.Request) {
		s.result(w, map[string]bool{"ok": true}, s.DB.AdminRiskRevokeKey(r.Context(), current(r).ID, r.PathValue("id")))
	}))
}

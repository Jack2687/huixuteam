package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"llm-refund/internal/refundprovider"
	"llm-refund/internal/store"
	"llm-refund/webui"
)

type userKey struct{}
type Server struct {
	DB     *store.Store
	Secure bool
}

func New(db *store.Store, secure bool) http.Handler {
	s := &Server{DB: db, Secure: secure}
	m := http.NewServeMux()
	m.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, map[string]string{"status": "ok"}) })
	m.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if db.Pool.Ping(ctx) != nil {
			fail(w, 503, "数据库尚未就绪")
			return
		}
		provider := "paused_or_unconfigured"
		if db.ProviderReady(ctx) {
			provider = "ready"
		}
		respond(w, 200, map[string]string{"status": "ready", "refund_provider": provider})
	})
	m.HandleFunc("POST /api/auth/login", s.login)
	m.Handle("GET /api/auth/me", s.auth(false, func(w http.ResponseWriter, r *http.Request) { respond(w, 200, current(r)) }))
	m.Handle("POST /api/auth/logout", s.auth(false, s.logout))
	m.Handle("GET /api/dashboard", s.auth(false, func(w http.ResponseWriter, r *http.Request) {
		v, e := s.DB.Dashboard(r.Context(), current(r).ID)
		s.result(w, v, e)
	}))
	for _, endpoint := range []struct {
		path, withdraw string
		key            bool
	}{{"/api/refunds", "cancel", false}, {"/open/v1/requests", "withdraw", true}} {
		m.Handle("GET "+endpoint.path, s.auth(endpoint.key, s.refunds))
		m.Handle("POST "+endpoint.path, s.auth(endpoint.key, s.createRefunds))
		m.Handle("GET "+endpoint.path+"/{id}", s.auth(endpoint.key, s.detail))
		m.Handle("POST "+endpoint.path+"/{id}/"+endpoint.withdraw, s.auth(endpoint.key, s.cancel))
	}
	m.Handle("GET /api/billing", s.auth(false, s.billing))
	m.Handle("GET /api/keys", s.auth(false, func(w http.ResponseWriter, r *http.Request) {
		v, e := s.DB.Keys(r.Context(), current(r).ID)
		s.result(w, map[string]any{"items": v}, e)
	}))
	m.Handle("POST /api/keys", s.auth(false, s.createKey))
	m.Handle("DELETE /api/keys/{id}", s.auth(false, func(w http.ResponseWriter, r *http.Request) {
		e := s.DB.RevokeKey(r.Context(), current(r).ID, r.PathValue("id"))
		s.result(w, map[string]bool{"ok": true}, e)
	}))
	m.Handle("GET /api/notifications", s.auth(false, func(w http.ResponseWriter, r *http.Request) {
		v, e := s.DB.Notifications(r.Context(), current(r).ID)
		s.result(w, map[string]any{"items": v}, e)
	}))
	m.Handle("POST /api/notifications/read", s.auth(false, func(w http.ResponseWriter, r *http.Request) {
		s.result(w, map[string]bool{"ok": true}, s.DB.ReadNotifications(r.Context(), current(r).ID))
	}))
	s.adminRoutes(m)
	s.executionRoutes(m)
	s.announcementRoutes(m)
	s.riskRoutes(m)
	// Unknown API routes must never return an HTML 200 response.
	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "接口不存在") })
	m.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "接口不存在") })
	m.HandleFunc("/open/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "接口不存在") })
	assets, _ := fs.Sub(webui.Static, "static")
	files := http.FileServer(http.FS(assets))
	m.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			fail(w, http.StatusMethodNotAllowed, "请求方法不被允许")
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(assets, path); err == nil {
			if strings.HasPrefix(path, "assets/admin-") {
				permission := "admin.overview.read"
				if strings.HasPrefix(path, "assets/admin-execution-") {
					permission = "admin.refund.execute"
				}
				if strings.HasPrefix(path, "assets/admin-risk-") {
					permission = "admin.risk.manage"
				}
				w.Header().Set("Cache-Control", "private, no-store")
				w.Header().Set("Vary", "Cookie")
				s.require(permission, files.ServeHTTP).ServeHTTP(w, r)
				return
			}
			if strings.HasPrefix(path, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}
		if path != "developers/reference" && !strings.HasPrefix(path, "workspace/") {
			http.NotFound(w, r)
			return
		}
		b, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			fail(w, 503, "请先构建前端资源")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	}))
	return s.riskMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" && !strings.HasPrefix(r.URL.Path, "/open/v1/") {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, e := url.Parse(origin)
				if e != nil || u.Host != r.Host {
					fail(w, 403, "请求来源不被允许")
					return
				}
			}
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				fail(w, 403, "请求来源不被允许")
				return
			}
		}
		defer func() {
			if e := recover(); e != nil {
				slog.Error("request panic", "path", r.URL.Path)
				fail(w, 500, "服务暂时不可用")
			}
		}()
		m.ServeHTTP(w, r)
	}))
}
func current(r *http.Request) store.User { return r.Context().Value(userKey{}).(store.User) }
func (s *Server) auth(key bool, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var token string
		if key {
			token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if token == r.Header.Get("Authorization") {
				token = ""
			}
		} else {
			if c, e := r.Cookie("refund_session"); e == nil {
				token = c.Value
			}
		}
		if token == "" {
			fail(w, 401, "请先登录或提供有效 API 密钥")
			return
		}
		u, err := s.DB.Authenticate(r.Context(), token, key)
		if err != nil {
			fail(w, 401, "登录已过期或密钥已失效")
			return
		}
		if !s.checkAuthenticatedRisk(w, r, u, token, key) {
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
	})
}
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, message string) {
	respond(w, status, map[string]string{"error": message})
}
func (s *Server) result(w http.ResponseWriter, v any, err error) {
	if err == nil {
		respond(w, 200, v)
		return
	}
	switch {
	case errors.Is(err, store.ErrSubmissionPaused):
		fail(w, 403, "当前账户暂不能提交新申请，已受理申请继续处理，请联系服务专员")
	case errors.Is(err, store.ErrForbidden):
		fail(w, 403, err.Error())
	case errors.Is(err, store.ErrInvalid):
		fail(w, 400, err.Error())
	case errors.Is(err, store.ErrNotFound):
		fail(w, 404, err.Error())
	case errors.Is(err, store.ErrState), errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrDuplicate):
		fail(w, 409, err.Error())
	case errors.Is(err, store.ErrBalance):
		fail(w, 402, err.Error())
	default:
		// Driver errors can include raw rows, SQL parameters or credentials.
		// Neither the HTTP response nor application logs may relay them.
		slog.Error("database operation failed")
		fail(w, 500, "操作失败，请稍后重试")
	}
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		fail(w, 415, "请使用 application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		fail(w, 400, "请求格式不正确或超过大小限制")
		return false
	}
	if d.Decode(&struct{}{}) != io.EOF {
		fail(w, 400, "请求必须只包含一个 JSON 对象")
		return false
	}
	return true
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Email) > 254 || len(body.Password) > 72 {
		fail(w, 400, "邮箱或密码格式不正确")
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	meta := riskMeta(r)
	if meta == nil {
		fail(w, 503, "服务暂时不可用")
		return
	}
	meta.Observation.Account = safeLoginAccount(email)
	identity := store.RiskIdentity{Type: "login", ID: email, Label: meta.Observation.Account}
	if !s.applyRiskChecks(w, r, []store.RiskConsumption{{Rule: "login", Policy: meta.Config.Rules["login"], Identity: identity}}) {
		return
	}
	u, err := s.DB.Login(r.Context(), body.Email, body.Password)
	if err != nil {
		meta.Kind = "login_failed"
		fail(w, 401, "邮箱或密码不正确")
		return
	}
	meta.Kind = "login_success"
	meta.Observation.UserID = u.ID
	meta.Observation.Account = u.Email
	if err = s.DB.ClearLoginRisk(r.Context(), email); err != nil {
		s.result(w, nil, err)
		return
	}
	token, err := s.DB.NewSession(r.Context(), u.ID)
	if err != nil {
		s.result(w, nil, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "refund_session", Value: token, Path: "/", HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteLaxMode, MaxAge: 7 * 24 * 3600})
	respond(w, 200, u)
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	c, _ := r.Cookie("refund_session")
	if err := s.DB.Logout(r.Context(), c.Value); err != nil {
		s.result(w, nil, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "refund_session", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteLaxMode})
	respond(w, 200, map[string]bool{"ok": true})
}
func pagination(r *http.Request) (int, int) {
	p, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if p < 1 {
		p = 1
	}
	if p > 100000 {
		p = 100000
	}
	if size < 1 || size > 100 {
		size = 20
	}
	return p, size
}
func (s *Server) refunds(w http.ResponseWriter, r *http.Request) {
	p, size := pagination(r)
	q := r.URL.Query().Get("q")
	if len(q) > 254 {
		fail(w, 400, "搜索内容过长")
		return
	}
	items, total, err := s.DB.ListRefunds(r.Context(), current(r).ID, q, r.URL.Query().Get("status"), p, size)
	s.result(w, map[string]any{"items": items, "total": total, "page": p, "size": size}, err)
}
func (s *Server) createRefunds(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Authorized bool                     `json:"authorized"`
		Accounts   []refundprovider.Account `json:"accounts"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !body.Authorized {
		fail(w, 400, "请先确认账号授权")
		return
	}
	if err := store.ValidateAccounts(body.Accounts); err != nil {
		fail(w, 400, err.Error())
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 8 || len(key) > 128 {
		fail(w, 400, "请提供长度为 8～128 的 Idempotency-Key")
		return
	}
	ids, replayed, err := s.DB.CreateRefunds(r.Context(), current(r).ID, key, body.Accounts)
	if err != nil {
		if errors.Is(err, store.ErrSubmissionPaused) {
			if meta := riskMeta(r); meta != nil {
				meta.Kind = "paused"
			}
		}
		s.result(w, nil, err)
		return
	}
	respond(w, 202, map[string]any{"ids": ids, "replayed": replayed, "status": "queued", "provider_ready": s.DB.ProviderReady(r.Context()), "message": "申请已保存，等待处理。"})
}
func (s *Server) detail(w http.ResponseWriter, r *http.Request) {
	uid := current(r).ID
	id := r.PathValue("id")
	item, err := s.DB.Refund(r.Context(), uid, id)
	if err != nil {
		s.result(w, nil, err)
		return
	}
	events, err := s.DB.Events(r.Context(), uid, id)
	if err != nil {
		s.result(w, nil, err)
		return
	}
	s.result(w, map[string]any{"refund": item, "events": events, "provider_ready": s.DB.ProviderReady(r.Context())}, err)
}
func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	uid := current(r).ID
	id := r.PathValue("id")
	item, err := s.DB.Refund(r.Context(), uid, id)
	if err != nil {
		s.result(w, nil, err)
		return
	}
	if item.Status != "queued" && item.Status != "cancelled" {
		s.result(w, nil, store.ErrState)
		return
	}
	s.result(w, map[string]bool{"ok": true}, s.DB.Transition(r.Context(), uid, id, "cancelled", "申请已取消，冻结积分已释放。"))
}
func (s *Server) billing(w http.ResponseWriter, r *http.Request) {
	p, size := pagination(r)
	items, total, err := s.DB.Billing(r.Context(), current(r).ID, p, size)
	s.result(w, map[string]any{"items": items, "total": total, "page": p, "size": size}, err)
}
func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(strings.TrimSpace(body.Name)) == 0 || len(body.Name) > 80 {
		fail(w, 400, "密钥名称长度须为 1～80 字节")
		return
	}
	token, err := s.DB.CreateKey(r.Context(), current(r).ID, body.Name)
	if err != nil {
		s.result(w, nil, err)
		return
	}
	respond(w, 201, map[string]string{"token": token})
}

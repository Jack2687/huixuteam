package store

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
)

type RiskRule struct {
	Mode            string `json:"mode"`
	Limit           int    `json:"limit"`
	WindowSeconds   int    `json:"window_seconds"`
	CooldownSeconds int    `json:"cooldown_seconds"`
}

type RiskSettings struct {
	Enabled               bool                `json:"enabled"`
	Rules                 map[string]RiskRule `json:"rules"`
	RetentionDays         int                 `json:"retention_days"`
	RefreshSeconds        int                 `json:"refresh_seconds"`
	ScanSeconds           int                 `json:"scan_seconds"`
	BehaviorWindowSeconds int                 `json:"behavior_window_seconds"`
	LoginSourceAccounts   int                 `json:"login_source_accounts"`
	DuplicateCount        int                 `json:"duplicate_count"`
	CancelCount           int                 `json:"cancel_count"`
	FailureMinCount       int                 `json:"failure_min_count"`
	FailurePercent        int                 `json:"failure_percent"`
	QueueCount            int                 `json:"queue_count"`
	QueueSeconds          int                 `json:"queue_seconds"`
	ProcessingSeconds     int                 `json:"processing_seconds"`
	LargeAdjustment       int64               `json:"large_adjustment"`
	FinanceChecks         bool                `json:"finance_checks"`
	TrustedProxies        []string            `json:"trusted_proxies"`
	Revision              int64               `json:"revision"`
}

var RiskRuleNames = map[string]string{"login": "登录尝试", "redeem": "兑换码兑换", "submit": "提交申请", "query": "查询与轮询", "write": "其他业务操作", "account": "客户总请求量", "api_key": "单个 API 密钥"}

func DefaultRiskSettings() RiskSettings {
	return RiskSettings{Enabled: true, Rules: map[string]RiskRule{
		"login": {"enforce", 10, 900, 900}, "redeem": {"enforce", 20, 60, 60},
		"submit": {"observe", 30, 60, 60}, "query": {"observe", 300, 60, 60},
		"write": {"observe", 60, 60, 60}, "account": {"observe", 600, 60, 60}, "api_key": {"observe", 300, 60, 60},
	}, RetentionDays: 30, RefreshSeconds: 10, ScanSeconds: 60, BehaviorWindowSeconds: 900,
		LoginSourceAccounts: 10, DuplicateCount: 10, CancelCount: 10, FailureMinCount: 5, FailurePercent: 50,
		QueueCount: 20, QueueSeconds: 600, ProcessingSeconds: 900, LargeAdjustment: 100000, FinanceChecks: true, TrustedProxies: []string{}}
}

func (s *Store) RiskSettings(ctx context.Context) (RiskSettings, error) {
	cfg := DefaultRiskSettings()
	var raw []byte
	var revision int64
	err := s.Pool.QueryRow(ctx, "SELECT config,revision FROM risk_settings WHERE id").Scan(&raw, &revision)
	if err == nil {
		err = json.Unmarshal(raw, &cfg)
	}
	cfg.Revision = revision
	if cfg.TrustedProxies == nil {
		cfg.TrustedProxies = []string{}
	}
	return cfg, err
}

func validateRiskSettings(v RiskSettings) error {
	if len(v.Rules) != len(RiskRuleNames) {
		return invalid("请完整设置所有限流规则")
	}
	for name, rule := range v.Rules {
		if RiskRuleNames[name] == "" || !oneOf(rule.Mode, "off", "observe", "enforce") || rule.Limit < 1 || rule.Limit > 100000 || rule.WindowSeconds < 1 || rule.WindowSeconds > 86400 || rule.CooldownSeconds < 1 || rule.CooldownSeconds > 86400 {
			return invalid("限流次数为 1～100000，窗口和限制时长为 1～86400 秒")
		}
	}
	if v.RetentionDays < 1 || v.RetentionDays > 365 || v.RefreshSeconds < 5 || v.RefreshSeconds > 300 || v.ScanSeconds < 10 || v.ScanSeconds > 3600 || v.BehaviorWindowSeconds < 60 || v.BehaviorWindowSeconds > 86400 || v.LoginSourceAccounts < 2 || v.LoginSourceAccounts > 10000 || v.DuplicateCount < 1 || v.DuplicateCount > 10000 || v.CancelCount < 1 || v.CancelCount > 10000 || v.FailureMinCount < 1 || v.FailureMinCount > 10000 || v.FailurePercent < 1 || v.FailurePercent > 100 || v.QueueCount < 1 || v.QueueCount > 100000 || v.QueueSeconds < 60 || v.QueueSeconds > 604800 || v.ProcessingSeconds < 60 || v.ProcessingSeconds > 604800 || v.LargeAdjustment < 100 || v.LargeAdjustment > 100000000 {
		return invalid("风控配置超出允许范围")
	}
	if len(v.TrustedProxies) > 64 {
		return invalid("可信代理最多 64 项")
	}
	for _, address := range v.TrustedProxies {
		if _, err := netip.ParseAddr(address); err == nil {
			continue
		}
		p, err := netip.ParsePrefix(address)
		if err != nil || p.Bits() == 0 {
			return invalid("可信代理须为 IP 或明确的网段，不能信任全部地址")
		}
	}
	return nil
}

func (s *Store) SaveRiskSettings(ctx context.Context, actor string, v RiskSettings) error {
	if v.TrustedProxies == nil {
		v.TrustedProxies = []string{}
	}
	if err := validateRiskSettings(v); err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = adminLock(ctx, tx, actor); err != nil {
		return err
	}
	var oldraw []byte
	var rev int64
	if err = tx.QueryRow(ctx, "SELECT config,revision FROM risk_settings WHERE id FOR UPDATE").Scan(&oldraw, &rev); err != nil {
		return err
	}
	if rev != v.Revision {
		return invalid("配置已被其他管理员修改，请重新载入后保存")
	}
	old := DefaultRiskSettings()
	if err = json.Unmarshal(oldraw, &old); err != nil {
		return err
	}
	for rule, value := range v.Rules {
		if value != old.Rules[rule] {
			if _, err = tx.Exec(ctx, "DELETE FROM risk_buckets WHERE rule=$1", rule); err != nil {
				return err
			}
		}
	}
	v.Revision = rev + 1
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE risk_settings SET config=$1,revision=revision+1,updated_at=now() WHERE id", raw); err != nil {
		return err
	}
	if err = audit(ctx, tx, actor, "risk.settings", "settings", map[string]any{"revision": v.Revision}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type RiskUserControl struct {
	UserID      string         `json:"user_id"`
	Paused      bool           `json:"paused"`
	PausedUntil *time.Time     `json:"paused_until"`
	Limits      map[string]int `json:"limits"`
}

func (s *Store) RiskUserControl(ctx context.Context, id string) (RiskUserControl, error) {
	v := RiskUserControl{UserID: id, Limits: map[string]int{}}
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT COALESCE(c.paused,false),c.paused_until,COALESCE(c.limits,'{}'::jsonb) FROM users u LEFT JOIN risk_user_controls c ON c.user_id=u.id WHERE u.id=$1 AND u.deleted_at IS NULL`, id).Scan(&v.Paused, &v.PausedUntil, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(raw, &v.Limits)
	}
	return v, err
}

func (s *Store) SaveRiskUserControl(ctx context.Context, actor string, v RiskUserControl) error {
	if len(v.Limits) > 6 {
		return invalid("客户规则数量无效")
	}
	for key, value := range v.Limits {
		if key == "login" || RiskRuleNames[key] == "" || value < 1 || value > 100000 {
			return invalid("客户次数覆盖值须为 1～100000")
		}
	}
	if !v.Paused {
		v.PausedUntil = nil
	}
	if v.PausedUntil != nil && (v.PausedUntil.Before(time.Now()) || v.PausedUntil.After(time.Now().Add(365*24*time.Hour))) {
		return invalid("请选择未来一年内的恢复时间，留空表示手动恢复")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = adminLock(ctx, tx, actor); err != nil {
		return err
	}
	var id string
	if err = tx.QueryRow(ctx, "SELECT id FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE", v.UserID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	raw, err := json.Marshal(v.Limits)
	if err != nil {
		return err
	}
	if v.Limits == nil {
		raw = []byte("{}")
	}
	_, err = tx.Exec(ctx, `INSERT INTO risk_user_controls(user_id,paused,paused_until,limits) VALUES($1,$2,$3,$4) ON CONFLICT(user_id) DO UPDATE SET paused=excluded.paused,paused_until=excluded.paused_until,limits=excluded.limits,updated_at=now()`, v.UserID, v.Paused, v.PausedUntil, raw)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "DELETE FROM risk_buckets WHERE user_id=$1 AND rule<>'login'", v.UserID); err != nil {
		return err
	}
	if err = audit(ctx, tx, actor, "risk.customer", v.UserID, map[string]any{"paused": v.Paused, "paused_until": v.PausedUntil, "limits": v.Limits}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

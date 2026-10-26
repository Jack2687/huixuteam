package store

import (
	"context"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
)

// These are business labels only. Never derive them from a URL, upstream error,
// HTTP body, account credentials or free-form operational logs.
var RiskOperations = map[string]string{"login": "账户登录", "logout": "退出登录", "submit": "提交申请", "query": "业务查询", "cancel": "撤回申请", "redeem": "兑换积分", "keys": "密钥管理", "admin": "后台管理", "risk": "风控管理", "resources": "服务配置", "unknown": "其他业务访问"}
var riskAdminActions = map[string]string{
	"user.create": "新增客户账户", "user.update": "调整账户资料或权限", "user.delete": "删除客户账户", "user.password_reset": "重置账户密码",
	"balance.credit": "增加客户积分", "balance.debit": "扣减客户积分", "code.generate": "生成兑换码", "code.update": "调整兑换码", "code.delete": "删除兑换码", "code.export": "导出兑换码",
	"risk.settings": "调整风控配置", "risk.customer": "调整客户风控规则", "risk.release": "解除访问限制", "risk.resolve": "标记风控事件已处理", "risk.key_revoke": "撤销客户 API 密钥",
	"refund.settings": "调整服务调度设置", "refund.proxy.save": "调整服务资源", "refund.proxy.delete": "移除服务资源", "refund.mother.save": "调整服务资源", "refund.mother.delete": "移除服务资源",
}

func init() {
	for action, message := range riskAdminActions {
		RiskEventKinds["admin_"+Hash(action)[:12]] = struct{ Category, Severity, Message string }{"admin", "info", message}
	}
}

var RiskEventKinds = map[string]struct{ Category, Severity, Message string }{
	"login_success": {"login", "info", "登录成功"}, "login_failed": {"login", "warning", "登录校验失败"},
	"auth_failed": {"access", "warning", "会话或 API 密钥校验失败"}, "denied": {"access", "warning", "请求权限或来源校验未通过"},
	"rate_blocked": {"limit", "warning", "访问频率触发限制"}, "rate_observed": {"limit", "warning", "访问频率达到告警阈值，未拦截"},
	"duplicate": {"application", "warning", "申请提交存在重复或幂等冲突"}, "cancelled": {"application", "info", "申请已撤回"},
	"redeem_failed": {"finance", "warning", "兑换未成功"}, "server_error": {"access", "critical", "业务接口返回服务错误"},
	"paused": {"limit", "warning", "客户已暂停提交新申请"}, "admin_change": {"admin", "info", "管理操作已执行"},
	"login_source":   {"login", "warning", "同一已核实来源短时间尝试多个账号"},
	"duplicate_many": {"application", "warning", "客户重复提交较多"}, "cancel_many": {"application", "warning", "客户短时间撤回较多"},
	"failure_rate": {"application", "warning", "客户近期申请未完成比例较高"}, "queue_many": {"application", "warning", "排队申请数量达到关注阈值"},
	"queue_long": {"application", "warning", "申请排队时间较长"}, "processing_long": {"application", "warning", "申请办理时间较长"},
	"large_adjustment": {"finance", "warning", "积分调整金额达到关注阈值"}, "frozen_mismatch": {"finance", "critical", "预留积分与未结算申请金额不一致"},
	"settlement_mismatch": {"finance", "critical", "服务费结算与账单记录不一致"},
}

type RiskObservation struct {
	UserID, Account, KeyID, SourceIP, Operation, Channel, TargetID, Rule string
	SourceVerified                                                       bool
	Status                                                               int
	DurationMS                                                           int64
}

func riskEventTx(ctx context.Context, tx pgx.Tx, kind string, o RiskObservation, dedupe string) error {
	definition, ok := RiskEventKinds[kind]
	if !ok {
		return invalid("风控事件类型无效")
	}
	if RiskOperations[o.Operation] == "" {
		o.Operation = "unknown"
	}
	if _, err := netip.ParseAddr(o.SourceIP); err != nil {
		o.SourceIP = ""
		o.SourceVerified = false
	}
	if RiskRuleNames[o.Rule] == "" {
		o.Rule = ""
	}
	_, err := tx.Exec(ctx, `INSERT INTO risk_events(dedupe,category,kind,severity,user_id,account,key_id,source_ip,source_verified,operation,target_id,rule,status)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
 ON CONFLICT(dedupe) DO UPDATE SET occurrences=risk_events.occurrences+1,last_seen=now(),status=excluded.status,resolved_at=NULL,resolved_by=''`, dedupe, definition.Category, kind, definition.Severity, o.UserID, o.Account, o.KeyID, o.SourceIP, o.SourceVerified, o.Operation, o.TargetID, o.Rule, o.Status)
	return err
}

func (s *Store) RecordRiskRequest(ctx context.Context, o RiskObservation, kind string) error {
	if RiskOperations[o.Operation] == "" {
		o.Operation = "unknown"
	}
	if !oneOf(o.Channel, "web", "api") {
		o.Channel = "web"
	}
	if o.DurationMS < 0 {
		o.DurationMS = 0
	}
	if o.DurationMS > 3600000 {
		o.DurationMS = 3600000
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO risk_traffic(minute,user_id,key_id,operation,channel,status,requests,duration_ms)
 VALUES(date_trunc('minute',now()),$1,$2,$3,$4,$5,1,$6)
 ON CONFLICT(minute,user_id,key_id,operation,channel,status) DO UPDATE SET requests=risk_traffic.requests+1,duration_ms=risk_traffic.duration_ms+excluded.duration_ms`, o.UserID, o.KeyID, o.Operation, o.Channel, o.Status, o.DurationMS)
	if err != nil {
		return err
	}
	if kind != "" {
		// Consolidate repeated errors into minute-level events to bound storage.
		var minute string
		if err = tx.QueryRow(ctx, "SELECT date_trunc('minute',now())::text").Scan(&minute); err != nil {
			return err
		}
		dedupe := Hash(kind + "|" + o.UserID + "|" + o.Account + "|" + o.KeyID + "|" + o.SourceIP + "|" + o.Operation + "|" + o.Rule + "|" + minute)
		if err = riskEventTx(ctx, tx, kind, o, dedupe); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) ResolveRiskEvent(ctx context.Context, actor string, id int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = adminLock(ctx, tx, actor); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "UPDATE risk_events SET resolved_at=now(),resolved_by=$2 WHERE id=$1", id, actor)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = audit(ctx, tx, actor, "risk.resolve", "event", map[string]int64{"event_id": id}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type RiskEvent struct {
	ID             int64      `json:"id"`
	Category       string     `json:"category"`
	Kind           string     `json:"kind"`
	Severity       string     `json:"severity"`
	Message        string     `json:"message"`
	UserID         string     `json:"user_id"`
	UserName       string     `json:"user_name"`
	Account        string     `json:"account"`
	KeyID          string     `json:"key_id"`
	KeyName        string     `json:"key_name"`
	SourceIP       string     `json:"source_ip"`
	SourceVerified bool       `json:"source_verified"`
	Operation      string     `json:"operation"`
	Rule           string     `json:"rule"`
	Status         int        `json:"status"`
	Occurrences    int64      `json:"occurrences"`
	CreatedAt      time.Time  `json:"created_at"`
	LastSeen       time.Time  `json:"last_seen"`
	ResolvedAt     *time.Time `json:"resolved_at"`
}

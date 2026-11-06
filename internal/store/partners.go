package store

import (
	"context"
	"encoding/json"
	"strings"
)

type PartnerStage struct {
	UntilDays   int `json:"until_days"`
	RatePercent int `json:"rate_percent"`
}
type PartnerRules struct {
	Enabled bool           `json:"enabled"`
	Stages  []PartnerStage `json:"stages"`
}

func (s *Store) SavePartnerRules(ctx context.Context, actor string, v PartnerRules) error {
	if len(v.Stages) != 3 {
		return invalid("请配置三个奖励阶段")
	}
	day, rate := 0, 31
	for _, stage := range v.Stages {
		if stage.UntilDays <= day || stage.UntilDays > 365 || stage.RatePercent < 1 || stage.RatePercent >= rate {
			return invalid("阶段天数须递增且不超过365天，比例须逐段降低且为1%～30%")
		}
		day, rate = stage.UntilDays, stage.RatePercent
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = adminLock(ctx, tx, actor); e != nil {
		return e
	}
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "UPDATE site_settings SET invitations=$1,updated_at=now() WHERE id", raw); e != nil {
		return e
	}
	if e = audit(ctx, tx, actor, "partner.rules", "site", v); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func (s *Store) Partners(ctx context.Context, owner, search string, page, size int) (map[string]any, error) {
	search = strings.TrimSpace(search)
	if len(search) > 300 {
		return nil, invalid("搜索内容过长")
	}
	if owner != "" {
		search = ""
	}
	if page < 1 || page > 100000 || size < 1 || size > 100 {
		return nil, invalid("分页无效")
	}
	out := map[string]any{"page": page, "size": size}
	var rules json.RawMessage
	if e := s.Pool.QueryRow(ctx, "SELECT invitations FROM site_settings WHERE id").Scan(&rules); e != nil {
		return nil, e
	}
	out["rules"] = rules
	if owner != "" {
		var code string
		if e := s.Pool.QueryRow(ctx, "SELECT invite_code FROM users WHERE id=$1", owner).Scan(&code); e != nil {
			return nil, e
		}
		out["invite_code"] = code
	}
	filter := ` FROM users u JOIN users i ON i.id=u.invited_by LEFT JOIN
 (SELECT invitee_id,sum(amount)::bigint earned,count(*) orders,max(created_at) rewarded_at FROM partner_rewards GROUP BY invitee_id) r ON r.invitee_id=u.id
 WHERE ($1='' OR u.invited_by=$1) AND ($2='' OR u.email ILIKE '%'||$2||'%' OR i.email ILIKE '%'||$2||'%')`
	var total, qualified, earned int64
	if e := s.Pool.QueryRow(ctx, "SELECT count(*),count(r.invitee_id),COALESCE(sum(r.earned),0)::bigint"+filter, owner, search).Scan(&total, &qualified, &earned); e != nil {
		return nil, e
	}
	out["total"] = total
	out["stats"] = map[string]any{"invited": total, "qualified": qualified, "earned": earned}
	var rows json.RawMessage
	e := s.Pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(row),'[]') FROM (SELECT u.id,
 CASE WHEN $1='' THEN u.email ELSE substring(u.email from 1 for 2)||'***@'||split_part(u.email,'@',2) END AS email,
 CASE WHEN $1='' THEN i.email ELSE NULL END AS inviter,u.created_at,COALESCE(r.earned,0) earned,COALESCE(r.orders,0) orders,r.rewarded_at`+filter+` ORDER BY u.created_at DESC,u.id LIMIT $3 OFFSET $4) row`, owner, search, size, (page-1)*size).Scan(&rows)
	out["items"] = rows
	return out, e
}

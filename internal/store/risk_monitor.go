package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

type RiskMonitorFilter struct {
	Query, Category, State, Start, End string
	Page, Size                         int
}
type RiskSummary struct {
	Requests        int64 `json:"requests"`
	Errors          int64 `json:"errors"`
	Blocked         int64 `json:"blocked"`
	LoginFailures   int64 `json:"login_failures"`
	AverageMS       int64 `json:"average_ms"`
	Events          int   `json:"events"`
	Pending         int   `json:"pending"`
	Restrictions    int   `json:"restrictions"`
	PausedCustomers int   `json:"paused_customers"`
}
type RiskTraffic struct {
	UserID    string `json:"user_id"`
	UserName  string `json:"user_name"`
	Email     string `json:"email"`
	KeyID     string `json:"key_id"`
	KeyName   string `json:"key_name"`
	Operation string `json:"operation"`
	Channel   string `json:"channel"`
	Requests  int64  `json:"requests"`
	Errors    int64  `json:"errors"`
	Blocked   int64  `json:"blocked"`
	AverageMS int64  `json:"average_ms"`
}
type RiskRestriction struct {
	ID           string    `json:"id"`
	Rule         string    `json:"rule"`
	SubjectType  string    `json:"subject_type"`
	Label        string    `json:"label"`
	UserID       string    `json:"user_id"`
	KeyID        string    `json:"key_id"`
	Count        int64     `json:"count"`
	BlockedUntil time.Time `json:"blocked_until"`
}
type RiskCustomer struct {
	RiskUserControl
	Email string `json:"email"`
	Name  string `json:"name"`
}
type RiskMonitor struct {
	Items             []RiskEvent       `json:"items"`
	Total             int               `json:"total"`
	Page              int               `json:"page"`
	Size              int               `json:"size"`
	Summary           RiskSummary       `json:"summary"`
	Traffic           []RiskTraffic     `json:"traffic"`
	Restrictions      []RiskRestriction `json:"restrictions"`
	Customers         []RiskCustomer    `json:"customers"`
	GeneratedAt       time.Time         `json:"generated_at"`
	LastScan          time.Time         `json:"last_scan"`
	RefreshSeconds    int               `json:"refresh_seconds"`
	MonitoringEnabled bool              `json:"monitoring_enabled"`
}

func (s *Store) RiskMonitor(ctx context.Context, f RiskMonitorFilter) (RiskMonitor, error) {
	out := RiskMonitor{Items: []RiskEvent{}, Traffic: []RiskTraffic{}, Restrictions: []RiskRestriction{}, Customers: []RiskCustomer{}, Page: f.Page, Size: f.Size}
	if out.Page < 1 {
		out.Page = 1
	}
	if out.Page > 100000 {
		out.Page = 100000
	}
	if out.Size < 1 || out.Size > 100 {
		out.Size = 100
	}
	f.Query = strings.TrimSpace(f.Query)
	if len(f.Query) > 254 || !oneOf(f.Category, "", "login", "access", "limit", "application", "finance", "admin") || !oneOf(f.State, "", "pending", "resolved") {
		return out, invalid("风控筛选条件无效")
	}
	loc := time.FixedZone("Beijing", 28800)
	if f.Start == "" {
		f.Start = time.Now().In(loc).Format("2006-01-02")
	}
	if f.End == "" {
		f.End = time.Now().In(loc).Format("2006-01-02")
	}
	start, err := time.ParseInLocation("2006-01-02", f.Start, loc)
	if err != nil {
		return out, invalid("开始日期无效")
	}
	end, err := time.ParseInLocation("2006-01-02", f.End, loc)
	if err != nil {
		return out, invalid("结束日期无效")
	}
	end = end.AddDate(0, 0, 1)
	if !start.Before(end) || end.Sub(start) > 366*24*time.Hour {
		return out, invalid("日期范围须在 366 天以内")
	}
	cfg, err := s.RiskSettings(ctx)
	if err != nil {
		return out, err
	}
	out.RefreshSeconds = cfg.RefreshSeconds
	out.MonitoringEnabled = cfg.Enabled
	const eventScope = ` FROM risk_events e LEFT JOIN users u ON u.id=e.user_id LEFT JOIN api_keys k ON k.id=e.key_id WHERE e.last_seen >= $1 AND e.last_seen < $2
 AND ($3='' OR position(lower($3) in lower(e.account||' '||COALESCE(u.name,'')||' '||COALESCE(u.email,'')||' '||COALESCE(k.name,'')||' '||e.source_ip))>0)
 AND ($4='' OR e.category=$4) AND ($5='' OR ($5='pending' AND e.resolved_at IS NULL AND e.severity<>'info') OR ($5='resolved' AND e.resolved_at IS NOT NULL))`
	args := []any{start, end, f.Query, f.Category, f.State}
	var rowsRaw []byte
	err = s.Pool.QueryRow(ctx, `WITH matched AS (SELECT e.id,e.category,e.kind,e.severity,e.user_id,COALESCE(u.name,'') user_name,e.account,e.key_id,COALESCE(k.name,'') key_name,e.source_ip,e.source_verified,e.operation,e.rule,e.status,e.occurrences,e.created_at,e.last_seen,e.resolved_at`+eventScope+`)
 SELECT (SELECT count(*) FROM matched),COALESCE((SELECT jsonb_agg(p ORDER BY p.last_seen DESC,p.id DESC) FROM (SELECT * FROM matched ORDER BY last_seen DESC,id DESC LIMIT $6 OFFSET $7) p),'[]'::jsonb)`, append(args, out.Size, (out.Page-1)*out.Size)...).Scan(&out.Total, &rowsRaw)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(rowsRaw, &out.Items); err != nil {
		return out, err
	}
	for i := range out.Items {
		if def, ok := RiskEventKinds[out.Items[i].Kind]; ok {
			out.Items[i].Message = def.Message
		} else {
			out.Items[i].Message = "业务安全事件"
		}
	}
	const trafficScope = ` FROM risk_traffic t LEFT JOIN users u ON u.id=t.user_id LEFT JOIN api_keys k ON k.id=t.key_id WHERE t.minute >= $1 AND t.minute < $2 AND ($3='' OR position(lower($3) in lower(COALESCE(u.email,'')||' '||COALESCE(u.name,'')||' '||COALESCE(k.name,'')))>0)`
	err = s.Pool.QueryRow(ctx, `SELECT COALESCE(sum(requests),0),COALESCE(sum(requests) FILTER(WHERE status>=400),0),COALESCE(sum(requests) FILTER(WHERE status=429),0),COALESCE(sum(requests) FILTER(WHERE operation='login' AND status=401),0),COALESCE(sum(duration_ms)/NULLIF(sum(requests),0),0)::bigint`+trafficScope, start, end, f.Query).Scan(&out.Summary.Requests, &out.Summary.Errors, &out.Summary.Blocked, &out.Summary.LoginFailures, &out.Summary.AverageMS)
	if err != nil {
		return out, err
	}
	err = s.Pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(p),'[]'::jsonb) FROM (SELECT t.user_id,COALESCE(u.name,'') user_name,COALESCE(u.email,'') email,t.key_id,COALESCE(k.name,'') key_name,t.operation,t.channel,sum(requests) requests,COALESCE(sum(requests) FILTER(WHERE status>=400),0) errors,COALESCE(sum(requests) FILTER(WHERE status=429),0) blocked,COALESCE(sum(duration_ms)/NULLIF(sum(requests),0),0)::bigint average_ms`+trafficScope+` GROUP BY t.user_id,u.name,u.email,t.key_id,k.name,t.operation,t.channel ORDER BY sum(requests) DESC,t.user_id LIMIT 100) p`, start, end, f.Query).Scan(&rowsRaw)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(rowsRaw, &out.Traffic); err != nil {
		return out, err
	}
	err = s.Pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE e.resolved_at IS NULL AND e.severity<>'info')`+eventScope, args...).Scan(&out.Summary.Events, &out.Summary.Pending)
	if err != nil {
		return out, err
	}
	err = s.Pool.QueryRow(ctx, `WITH active AS (SELECT id,rule,subject_type,label,user_id,key_id,count,blocked_until FROM risk_buckets WHERE mode='enforce' AND blocked_until>now()) SELECT (SELECT count(*) FROM active),COALESCE((SELECT jsonb_agg(p) FROM (SELECT * FROM active ORDER BY blocked_until DESC LIMIT 100) p),'[]'::jsonb)`).Scan(&out.Summary.Restrictions, &rowsRaw)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(rowsRaw, &out.Restrictions); err != nil {
		return out, err
	}
	err = s.Pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(p),'[]'::jsonb) FROM (SELECT c.user_id,c.paused,c.paused_until,c.limits,u.email,u.name FROM risk_user_controls c JOIN users u ON u.id=c.user_id WHERE u.deleted_at IS NULL AND ((c.paused AND (c.paused_until IS NULL OR c.paused_until>now())) OR c.limits<>'{}'::jsonb) ORDER BY c.updated_at DESC LIMIT 100) p`).Scan(&rowsRaw)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(rowsRaw, &out.Customers); err != nil {
		return out, err
	}
	err = s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM risk_user_controls WHERE paused AND (paused_until IS NULL OR paused_until>now())),last_scan,now() FROM risk_scan_state WHERE id`).Scan(&out.Summary.PausedCustomers, &out.LastScan, &out.GeneratedAt)
	return out, err
}

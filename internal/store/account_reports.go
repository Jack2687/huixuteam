package store

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// Report periods are Beijing calendar periods, independent of server timezone.
func AccountReportPeriod(period, anchor string, now time.Time) (time.Time, time.Time, error) {
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	d := now.In(loc)
	if anchor != "" {
		var err error
		d, err = time.ParseInLocation("2006-01-02", anchor, loc)
		if err != nil || d.Year() < 1900 || d.Year() > 9998 {
			return time.Time{}, time.Time{}, invalid("请选择有效日期")
		}
	}
	start := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
	switch period {
	case "", "day":
		return start, start.AddDate(0, 0, 1), nil
	case "week":
		start = start.AddDate(0, 0, -(int(start.Weekday())+6)%7)
		return start, start.AddDate(0, 0, 7), nil
	case "month":
		start = time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, loc)
		return start, start.AddDate(0, 1, 0), nil
	case "year":
		start = time.Date(d.Year(), 1, 1, 0, 0, 0, 0, loc)
		return start, start.AddDate(1, 0, 0), nil
	default:
		return time.Time{}, time.Time{}, invalid("统计周期须为日、周、月或年")
	}
}

type AccountReportMetrics struct {
	Recharged    int64 `json:"recharged"`
	Debited      int64 `json:"debited"`
	FreezeAdded  int64 `json:"freeze_added"`
	Released     int64 `json:"released"`
	Used         int64 `json:"used"`
	SettledCount int64 `json:"settled_count"`
	Balance      int64 `json:"balance"`
	Frozen       int64 `json:"frozen"`
	Available    int64 `json:"available"`
	Applications int64 `json:"applications"`
	Queued       int64 `json:"queued"`
	Processing   int64 `json:"processing"`
	Succeeded    int64 `json:"succeeded"`
	Attention    int64 `json:"attention"`
	Failed       int64 `json:"failed"`
	Cancelled    int64 `json:"cancelled"`
}
type AccountReportRow struct {
	AccountReportMetrics
	ID         string `json:"id"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Role       string `json:"role"`
	Disabled   bool   `json:"disabled"`
	ServiceFee int64  `json:"service_fee"`
}
type AccountReport struct {
	Items           []AccountReportRow   `json:"items"`
	Summary         AccountReportMetrics `json:"summary"`
	Total           int                  `json:"total"`
	Page            int                  `json:"page"`
	Size            int                  `json:"size"`
	Period          string               `json:"period"`
	Start           string               `json:"start"`
	End             string               `json:"end"` // inclusive calendar label
	Timezone        string               `json:"timezone"`
	GeneratedAt     time.Time            `json:"generated_at"`
	ExcludeAdmin    bool                 `json:"exclude_admin"`
	ExcludedUserIDs []string             `json:"excluded_user_ids"`
}

var reportAccountID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func reportExclusions(ids []string) ([]string, error) {
	if len(ids) > 200 {
		return nil, invalid("最多排除 200 个账户")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if !reportAccountID.MatchString(id) {
			return nil, invalid("排除账户参数无效")
		}
		if !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out, nil
}

type AccountReportOption struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type AccountReportOptions struct {
	Items []AccountReportOption `json:"items"`
	Total int                   `json:"total"`
}

// The selector shares report permissions and includes historical accounts,
// but exposes no credentials, balances or account-management capability.
func (s *Store) AdminAccountReportOptions(ctx context.Context, q string, excludeAdmin bool, page int) (AccountReportOptions, error) {
	q = strings.TrimSpace(q)
	if len(q) > 254 {
		return AccountReportOptions{}, invalid("搜索内容过长")
	}
	if page < 1 {
		page = 1
	}
	if page > 100000 {
		page = 100000
	}
	const query = `WITH selected AS (
 SELECT id,name,email FROM users WHERE (NOT $2 OR role<>'admin')
 AND ($1='' OR position(lower($1) in lower(email||' '||name))>0)
), page AS (SELECT * FROM selected ORDER BY email,id LIMIT 50 OFFSET $3)
 SELECT jsonb_build_object('items',COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY email,id) FROM page p),'[]'::jsonb),'total',(SELECT count(*) FROM selected))`
	var raw []byte
	if err := s.Pool.QueryRow(ctx, query, q, excludeAdmin, (page-1)*50).Scan(&raw); err != nil {
		return AccountReportOptions{}, err
	}
	var out AccountReportOptions
	err := json.Unmarshal(raw, &out)
	return out, err
}

// Aggregate each ledger/refund independently before joining users. A single SQL
// snapshot keeps paginated items and the full filtered summary consistent.
func (s *Store) AdminAccountReport(ctx context.Context, period, anchor, q string, excludeAdmin bool, page, size int, excludedIDs ...string) (AccountReport, error) {
	excludedIDs, err := reportExclusions(excludedIDs)
	if err != nil {
		return AccountReport{}, err
	}
	now := time.Now()
	start, end, err := AccountReportPeriod(period, anchor, now)
	if err != nil {
		return AccountReport{}, err
	}
	q = strings.TrimSpace(q)
	if len(q) > 254 {
		return AccountReport{}, invalid("搜索内容过长")
	}
	if period == "" {
		period = "day"
	}
	if page < 1 {
		page = 1
	}
	if page > 100000 {
		page = 100000
	}
	if size < 1 || size > 100 {
		size = 100
	}
	const query = `WITH selected AS (
 SELECT id,name,email,role,disabled,service_fee,balance,frozen,balance-frozen AS available FROM users
 WHERE (NOT $4 OR role<>'admin') AND ($3='' OR position(lower($3) in lower(email||' '||name))>0)
 AND NOT (id=ANY($7::text[]))
), funds AS (
 SELECT l.user_id,
 COALESCE(sum(amount) FILTER (WHERE kind IN ('credit','redeem')),0) AS recharged,
 COALESCE(-sum(amount) FILTER (WHERE kind='debit'),0) AS debited,
 COALESCE(sum(amount) FILTER (WHERE kind='freeze'),0) AS freeze_added,
 COALESCE(sum(amount) FILTER (WHERE kind='release'),0) AS released,
 COALESCE(-sum(amount) FILTER (WHERE kind='settlement'),0) AS used,
 count(*) FILTER (WHERE kind='settlement') AS settled_count
 FROM ledger l JOIN selected u ON u.id=l.user_id WHERE l.created_at >= $1 AND l.created_at < $2 GROUP BY l.user_id
), requests AS (
 SELECT r.user_id,count(*) AS applications,
 count(*) FILTER (WHERE status='queued') AS queued,
 count(*) FILTER (WHERE status IN ('processing','verifying')) AS processing,
 count(*) FILTER (WHERE status='succeeded') AS succeeded,
 count(*) FILTER (WHERE status='attention') AS attention,
 count(*) FILTER (WHERE status IN ('failed','banned')) AS failed,
 count(*) FILTER (WHERE status='cancelled') AS cancelled
 FROM refunds r JOIN selected u ON u.id=r.user_id WHERE r.created_at >= $1 AND r.created_at < $2 GROUP BY r.user_id
), report AS (
 SELECT u.*,COALESCE(f.recharged,0) AS recharged,COALESCE(f.debited,0) AS debited,
 COALESCE(f.freeze_added,0) AS freeze_added,COALESCE(f.released,0) AS released,
 COALESCE(f.used,0) AS used,COALESCE(f.settled_count,0) AS settled_count,
 COALESCE(r.applications,0) AS applications,COALESCE(r.queued,0) AS queued,
 COALESCE(r.processing,0) AS processing,COALESCE(r.succeeded,0) AS succeeded,
 COALESCE(r.attention,0) AS attention,COALESCE(r.failed,0) AS failed,COALESCE(r.cancelled,0) AS cancelled
 FROM selected u LEFT JOIN funds f ON f.user_id=u.id LEFT JOIN requests r ON r.user_id=u.id
), totals AS (
 SELECT COALESCE(sum(recharged),0) AS recharged,COALESCE(sum(debited),0) AS debited,
 COALESCE(sum(freeze_added),0) AS freeze_added,COALESCE(sum(released),0) AS released,
 COALESCE(sum(used),0) AS used,COALESCE(sum(settled_count),0) AS settled_count,
 COALESCE(sum(balance),0) AS balance,COALESCE(sum(frozen),0) AS frozen,COALESCE(sum(available),0) AS available,
 COALESCE(sum(applications),0) AS applications,COALESCE(sum(queued),0) AS queued,
 COALESCE(sum(processing),0) AS processing,COALESCE(sum(succeeded),0) AS succeeded,
 COALESCE(sum(attention),0) AS attention,COALESCE(sum(failed),0) AS failed,COALESCE(sum(cancelled),0) AS cancelled FROM report
), page AS (SELECT * FROM report ORDER BY used DESC,recharged DESC,email,id LIMIT $5 OFFSET $6)
 SELECT jsonb_build_object('items',COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY used DESC,recharged DESC,email,id) FROM page p),'[]'::jsonb),
 'total',(SELECT count(*) FROM report),'summary',(SELECT to_jsonb(t) FROM totals t))`
	var raw []byte
	if err := s.Pool.QueryRow(ctx, query, start, end, q, excludeAdmin, size, (page-1)*size, excludedIDs).Scan(&raw); err != nil {
		return AccountReport{}, err
	}
	var result AccountReport
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	result.Page, result.Size, result.Period = page, size, period
	result.Start, result.End = start.Format("2006-01-02"), end.AddDate(0, 0, -1).Format("2006-01-02")
	result.Timezone, result.GeneratedAt, result.ExcludeAdmin = "Asia/Shanghai", now, excludeAdmin
	result.ExcludedUserIDs = excludedIDs
	return result, nil
}

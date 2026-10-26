package store

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Scans only business records. It never reads private execution checkpoints or
// contacts an upstream service. Existing applications are never paused here.
func (s *Store) ScanRisk(ctx context.Context) error {
	cfg, err := s.RiskSettings(ctx)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var locked bool
	if err = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(718374025)").Scan(&locked); err != nil || !locked {
		return err
	}
	var due bool
	if err = tx.QueryRow(ctx, "SELECT last_scan <= now()-($1*interval '1 second') FROM risk_scan_state WHERE id FOR UPDATE", cfg.ScanSeconds).Scan(&due); err != nil || !due {
		return err
	}
	// Every source query returns only safe identities and a stable business ID.
	type scan struct {
		kind, query string
		args        []any
	}
	jobs := []scan{
		{"login_source", `SELECT '', '',source_ip,source_ip FROM risk_events WHERE kind='login_failed' AND source_verified AND last_seen>now()-($1*interval '1 second') GROUP BY source_ip HAVING count(DISTINCT account)>=$2`, []any{cfg.BehaviorWindowSeconds, cfg.LoginSourceAccounts}},
		{"duplicate_many", `SELECT e.user_id,COALESCE(u.email,''),'',e.user_id FROM risk_events e LEFT JOIN users u ON u.id=e.user_id WHERE kind='duplicate' AND last_seen>now()-($1*interval '1 second') GROUP BY e.user_id,u.email HAVING sum(occurrences)>=$2`, []any{cfg.BehaviorWindowSeconds, cfg.DuplicateCount}},
		{"cancel_many", `SELECT r.user_id,u.email,'',r.user_id FROM refunds r JOIN users u ON u.id=r.user_id WHERE r.status='cancelled' AND r.updated_at>now()-($1*interval '1 second') GROUP BY r.user_id,u.email HAVING count(*)>=$2`, []any{cfg.BehaviorWindowSeconds, cfg.CancelCount}},
		{"failure_rate", `SELECT r.user_id,u.email,'',r.user_id FROM refunds r JOIN users u ON u.id=r.user_id WHERE r.status IN ('succeeded','failed','banned') AND r.updated_at>now()-($1*interval '1 second') GROUP BY r.user_id,u.email HAVING count(*)>=$2 AND count(*) FILTER(WHERE r.status IN ('failed','banned'))*100.0/count(*) >= $3`, []any{cfg.BehaviorWindowSeconds, cfg.FailureMinCount, cfg.FailurePercent}},
		{"queue_many", `SELECT user_id,u.email,'',user_id FROM refunds r JOIN users u ON u.id=r.user_id WHERE r.status='queued' GROUP BY user_id,u.email HAVING count(*)>=$1`, []any{cfg.QueueCount}},
		{"queue_long", `SELECT r.user_id,u.email,'',r.id FROM refunds r JOIN users u ON u.id=r.user_id WHERE r.status='queued' AND r.created_at<now()-($1*interval '1 second')`, []any{cfg.QueueSeconds}},
		{"processing_long", `SELECT r.user_id,u.email,'',r.id FROM refunds r JOIN users u ON u.id=r.user_id WHERE r.status IN ('processing','verifying') AND EXISTS(SELECT 1 FROM refund_events e WHERE e.refund_id=r.id AND NOT e.internal AND e.message IN ('开始处理','正在继续处理') AND e.created_at<now()-($1*interval '1 second'))`, []any{cfg.ProcessingSeconds}},
		{"large_adjustment", `SELECT l.user_id,u.email,'',l.id::text FROM ledger l JOIN users u ON u.id=l.user_id WHERE l.kind IN ('credit','debit') AND abs(l.amount)>=$1 AND l.created_at>now()-($2*interval '1 day')`, []any{cfg.LargeAdjustment, cfg.RetentionDays}},
		{"admin_change", `SELECT a.actor_id,u.email,'',a.action||':'||a.id::text FROM audit_logs a JOIN users u ON u.id=a.actor_id WHERE a.created_at>now()-($1*interval '1 day') AND a.action IN ('user.create','user.update','user.delete','user.password_reset','balance.credit','balance.debit','code.generate','code.update','code.delete','code.export','risk.settings','risk.customer','risk.release','risk.resolve','risk.key_revoke','refund.settings','refund.proxy.save','refund.proxy.delete','refund.mother.save','refund.mother.delete')`, []any{cfg.RetentionDays}},
	}
	if cfg.FinanceChecks {
		jobs = append(jobs,
			scan{"frozen_mismatch", `SELECT u.id,u.email,'',u.id FROM users u WHERE u.frozen<>(SELECT COALESCE(sum(fee),0) FROM refunds r WHERE r.user_id=u.id AND r.fee_status='frozen')`, nil},
			scan{"settlement_mismatch", `SELECT r.user_id,u.email,'',r.id FROM refunds r JOIN users u ON u.id=r.user_id LEFT JOIN ledger l ON l.refund_id=r.id AND l.kind='settlement' GROUP BY r.id,u.email HAVING (r.fee_status='settled' AND (count(l.id)<>1 OR COALESCE(sum(l.amount),0)<>-r.fee)) OR (r.fee_status<>'settled' AND count(l.id)>0)`, nil})
	}
	if cfg.Enabled {
		for _, job := range jobs {
			rows, err := tx.Query(ctx, job.query, job.args...)
			if err != nil {
				return err
			}
			type record struct{ uid, email, ip, target string }
			records := []record{}
			for rows.Next() {
				var r record
				if err = rows.Scan(&r.uid, &r.email, &r.ip, &r.target); err != nil {
					rows.Close()
					return err
				}
				records = append(records, r)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			for _, r := range records {
				key := Hash("scan:" + job.kind + ":" + r.target)
				kind := job.kind
				if kind == "admin_change" {
					action, _, _ := strings.Cut(r.target, ":")
					if riskAdminActions[action] == "" {
						continue
					}
					kind = "admin_" + Hash(action)[:12]
				}
				// Acknowledged ongoing conditions stay acknowledged. Once a condition
				// clears, a later recurrence creates a separate actionable event.
				if job.kind != "admin_change" && job.kind != "large_adjustment" {
					var episode int64
					if err = tx.QueryRow(ctx, `INSERT INTO risk_conditions(id) VALUES($1)
 ON CONFLICT(id) DO UPDATE SET episode=risk_conditions.episode+CASE WHEN risk_conditions.active THEN 0 ELSE 1 END,active=true,seen_at=now() RETURNING episode`, key).Scan(&episode); err != nil {
						return err
					}
					key = Hash(fmt.Sprintf("%s:%d", key, episode))
				}
				var exists bool
				if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM risk_events WHERE dedupe=$1)", key).Scan(&exists); err != nil {
					return err
				}
				if exists {
					if job.kind != "admin_change" && job.kind != "large_adjustment" {
						if _, err = tx.Exec(ctx, "UPDATE risk_events SET last_seen=now() WHERE dedupe=$1", key); err != nil {
							return err
						}
					}
					continue
				}
				o := RiskObservation{UserID: r.uid, Account: r.email, SourceIP: r.ip, SourceVerified: r.ip != "", Operation: "query", TargetID: r.target}
				if job.kind == "admin_change" {
					o.Operation = "admin"
				}
				if err = riskEventTx(ctx, tx, kind, o, key); err != nil {
					return err
				}
			}
		}
		if _, err = tx.Exec(ctx, "UPDATE risk_conditions SET active=false WHERE active AND seen_at<now()"); err != nil {
			return err
		}
	}
	for _, table := range []string{"risk_events", "risk_traffic"} {
		column := "last_seen"
		if table == "risk_traffic" {
			column = "minute"
		}
		if _, err = tx.Exec(ctx, fmt.Sprintf("DELETE FROM %s WHERE %s<now()-($1*interval '1 day')", table, column), cfg.RetentionDays); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, "DELETE FROM risk_buckets WHERE window_until<now()-interval '1 day' AND (blocked_until IS NULL OR blocked_until<now())"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "DELETE FROM risk_conditions WHERE NOT active AND seen_at<now()-($1*interval '1 day')", cfg.RetentionDays); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE risk_scan_state SET last_scan=now() WHERE id"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RunRiskMonitor(ctx context.Context) {
	timer := time.NewTicker(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			check, cancel := context.WithTimeout(ctx, 15*time.Second)
			if err := s.ScanRisk(check); err != nil && ctx.Err() == nil {
				slog.Error("risk scan unavailable")
			}
			cancel()
		}
	}
}

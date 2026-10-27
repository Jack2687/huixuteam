package store

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"llm-refund/internal/refundprovider"
)

type RefundJob struct {
	MotherFingerprint string                 `json:"-"`
	RecoveryMode      string                 `json:"recovery_mode"`
	PersonalBaseline  *PersonalSnapshot      `json:"personal_baseline"`
	ID                string                 `json:"id"`
	UserID            string                 `json:"-"`
	LeaseToken        string                 `json:"-"`
	Step              string                 `json:"step"`
	ChildUserID       string                 `json:"child_user_id"`
	Account           refundprovider.Account `json:"account"`
	Mother            RefundMother           `json:"mother"`
	ChildProxy        string                 `json:"child_proxy"`
	MotherProxy       string                 `json:"mother_proxy"`
	Settings          RefundSettings         `json:"settings"`
}

// ClaimRefund serializes allocation in the database, not in process memory.
// A mother remains reserved even while its request needs human attention.
func (s *Store) ClaimRefund(ctx context.Context) (*RefundJob, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(718374023)"); err != nil {
		return nil, err
	}
	var cfg RefundSettings
	if err = tx.QueryRow(ctx, "SELECT enabled,max_parallel,login_attempts,retry_seconds,COALESCE(child_proxy_id,''),mother_interval_seconds,recovery,mother_failover FROM refund_settings WHERE id").Scan(&cfg.Enabled, &cfg.MaxParallel, &cfg.LoginAttempts, &cfg.RetrySeconds, &cfg.ChildProxyID, &cfg.MotherIntervalSeconds, &cfg.Recovery, &cfg.MotherFailover); err != nil {
		return nil, err
	}
	recoveryEnabled := cfg.Recovery != nil && cfg.Recovery.Enabled
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM refund_execution WHERE lease_until>now() AND NOT released").Scan(&count); err != nil {
		return nil, err
	}
	if count >= cfg.MaxParallel {
		return nil, nil
	}
	job := &RefundJob{Settings: cfg}
	// Recover an interrupted reservation first. Unknown submissions are never replayed.
	var oldToken, childProxyID, previousStatus string
	err = tx.QueryRow(ctx, `SELECT e.refund_id,r.user_id,e.mother_id,e.step,e.child_user_id,e.lease_token,COALESCE(e.child_proxy_id,''),e.recovery_mode,e.personal_baseline,r.status
 FROM refund_execution e JOIN refunds r ON r.id=e.refund_id JOIN refund_mothers m ON m.id=e.mother_id JOIN refund_proxies p ON p.id=m.proxy_id
 WHERE NOT e.released AND (r.status IN ('processing','verifying') OR (r.status='attention' AND $2 AND e.recovery_started_at IS NULL AND e.step IN ('submitting','accepted','removing')))
 AND (e.next_attempt_at IS NULL OR e.next_attempt_at<=now()) AND (e.recovery_mode='' OR $2)
 AND (e.lease_until IS NULL OR e.lease_until<now()) AND m.enabled AND p.enabled
 AND (e.step IN ('accepted','removing') OR e.recovery_mode='cleanup' OR EXISTS(SELECT 1 FROM refund_proxies cp WHERE cp.id=COALESCE(e.child_proxy_id,NULLIF($1,'')) AND cp.enabled))
 ORDER BY COALESCE(e.next_attempt_at,e.updated_at),e.refund_id LIMIT 1`, cfg.ChildProxyID, recoveryEnabled).Scan(&job.ID, &job.UserID, &job.Mother.ID, &job.Step, &job.ChildUserID, &oldToken, &childProxyID, &job.RecoveryMode, &job.PersonalBaseline, &previousStatus)
	existing := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if existing {
		if _, err = tx.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", job.UserID); err != nil {
			return nil, err
		}
		var live, released, eligible bool
		if err = tx.QueryRow(ctx, `SELECT e.step,e.lease_token,e.recovery_mode,e.personal_baseline,COALESCE(e.lease_until>now(),false),e.released,r.status,
 (e.next_attempt_at IS NULL OR e.next_attempt_at<=now()) AND (e.recovery_mode='' OR $2)
 AND (r.status IN ('processing','verifying') OR (r.status='attention' AND $2 AND e.recovery_started_at IS NULL AND e.step IN ('submitting','accepted','removing')))
 FROM refund_execution e JOIN refunds r ON r.id=e.refund_id WHERE e.refund_id=$1 FOR UPDATE OF r,e`, job.ID, recoveryEnabled).Scan(&job.Step, &oldToken, &job.RecoveryMode, &job.PersonalBaseline, &live, &released, &previousStatus, &eligible); err != nil {
			return nil, err
		}
		// A finishing worker may have scheduled a later attempt while this claim
		// waited for the user's lock. Never bypass that newly persisted interval.
		if live || released || !eligible {
			return nil, nil
		}
	}
	if existing && recoveryEnabled && (previousStatus == "attention" || oldToken != "") {
		if err = scheduleRefundRecovery(ctx, tx, job, job.Step, RefundDiagnostic{Code: "worker_interrupted", Retryable: true}); err != nil {
			return nil, err
		}
		return nil, tx.Commit(ctx)
	}
	if existing && oldToken != "" && job.Step != "accepted" && job.Step != "removing" {
		message := "任务中断，执行进度已保留，请管理员核实后续跑。"
		if job.Step == "submitting" {
			message = "停用提交结果待核实，未重发请求，也未移出团队。"
		}
		if _, err = tx.Exec(ctx, "INSERT INTO refund_events(refund_id,message,internal) VALUES($1,$2,true)", job.ID, message); err != nil {
			return nil, err
		}
		if err = transitionTx(ctx, tx, job.UserID, job.ID, "attention", "申请需要跟进，请联系服务专员。"); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, "UPDATE refund_execution SET lease_until=NULL,lease_token='',updated_at=now() WHERE refund_id=$1", job.ID); err != nil {
			return nil, err
		}
		return nil, tx.Commit(ctx)
	}
	if !existing {
		if !cfg.Enabled {
			return nil, nil
		}
		// Only new reservations wait. Persisted completion times survive restarts;
		// the latest setting also applies to mothers already between requests.
		// New and safely reassigned requests share one FIFO queue. A waiting switch
		// consumes no worker/mother and retains its original child proxy and fee.
		failoverEnabled := cfg.MotherFailover != nil && cfg.MotherFailover.Enabled
		err = tx.QueryRow(ctx, `SELECT r.id,r.user_id,m.id,COALESCE(e.child_proxy_id,NULLIF($3,''),'')
 FROM refunds r JOIN users u ON u.id=r.user_id LEFT JOIN refund_execution e ON e.refund_id=r.id
 JOIN LATERAL (
  SELECT m.id FROM refund_mothers m JOIN refund_proxies p ON p.id=m.proxy_id
  WHERE m.enabled AND p.enabled AND (m.token_expires_at IS NULL OR m.token_expires_at>now())
  AND (m.auth_failed_fingerprint IS NULL OR m.auth_failed_fingerprint<>md5(m.secret))
  AND NOT (m.id=ANY(COALESCE(e.failed_mothers,'{}'::text[])))
  AND NOT EXISTS(SELECT 1 FROM refund_execution busy WHERE busy.mother_id=m.id AND NOT busy.released)
  AND NOT EXISTS(SELECT 1 FROM refund_execution done WHERE done.mother_id=m.id AND done.completed_at>now()-($1 * interval '1 second'))
  ORDER BY m.updated_at,m.id LIMIT 1
 ) m ON true
 WHERE r.status='queued' AND NOT u.disabled
 AND (e.refund_id IS NULL OR ($2 AND e.released AND e.step='assigned' AND e.recovery_mode='mother_switch' AND e.next_attempt_at<=now()))
 AND EXISTS(SELECT 1 FROM refund_proxies cp WHERE cp.id=COALESCE(e.child_proxy_id,NULLIF($3,'')) AND cp.enabled)
 AND NOT EXISTS(SELECT 1 FROM refund_execution other JOIN refunds old ON old.id=other.refund_id
  WHERE lower(old.email)=lower(r.email) AND (NOT other.released OR other.accepted_at IS NOT NULL))
 ORDER BY r.created_at,r.id LIMIT 1`, cfg.MotherIntervalSeconds, failoverEnabled, cfg.ChildProxyID).Scan(&job.ID, &job.UserID, &job.Mother.ID, &childProxyID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if err = transitionTx(ctx, tx, job.UserID, job.ID, "processing", "开始处理"); err != nil {
			if errors.Is(err, ErrState) {
				return nil, nil
			}
			return nil, err
		}
		job.Step = "assigned"
		if _, err = tx.Exec(ctx, `INSERT INTO refund_execution(refund_id,mother_id,child_proxy_id) VALUES($1,$2,NULLIF($3,''))
 ON CONFLICT(refund_id) DO UPDATE SET mother_id=excluded.mother_id,released=false,recovery_mode='',next_attempt_at=NULL
 WHERE refund_execution.released AND refund_execution.step='assigned' AND refund_execution.recovery_mode='mother_switch'`, job.ID, job.Mother.ID, childProxyID); err != nil {
			return nil, err
		}
	}
	if childProxyID == "" {
		childProxyID = cfg.ChildProxyID
	}
	var childProxySecret []byte
	if job.Step != "accepted" && job.Step != "removing" && job.RecoveryMode != "cleanup" {
		err = tx.QueryRow(ctx, "SELECT secret FROM refund_proxies WHERE id=$1 AND enabled", childProxyID).Scan(&childProxySecret)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if err = s.openSecret("proxy:"+childProxyID, childProxySecret, &job.ChildProxy); err != nil {
			return nil, err
		}
	}
	job.LeaseToken = Token()
	if _, err = tx.Exec(ctx, "UPDATE refund_execution SET lease_token=$2,lease_until=now()+interval '90 seconds',child_proxy_id=COALESCE(child_proxy_id,NULLIF($3,'')),next_attempt_at=NULL,retry_count=retry_count+CASE WHEN recovery_mode='retry' THEN 1 ELSE 0 END,verify_count=verify_count+CASE WHEN recovery_mode='verify' THEN 1 ELSE 0 END,cleanup_count=cleanup_count+CASE WHEN recovery_mode='cleanup' THEN 1 ELSE 0 END,updated_at=now() WHERE refund_id=$1", job.ID, job.LeaseToken, childProxyID); err != nil {
		return nil, err
	}
	var motherSecret, proxySecret, accountSecret []byte
	err = tx.QueryRow(ctx, `SELECT m.name,m.email,m.team_id,m.proxy_id,m.seat_type,m.secret,p.secret,r.credentials,md5(m.secret) FROM refund_mothers m JOIN refund_proxies p ON p.id=m.proxy_id JOIN refunds r ON r.id=$2 WHERE m.id=$1`, job.Mother.ID, job.ID).Scan(&job.Mother.Name, &job.Mother.Email, &job.Mother.TeamID, &job.Mother.ProxyID, &job.Mother.SeatType, &motherSecret, &proxySecret, &accountSecret, &job.MotherFingerprint)
	if err != nil {
		return nil, err
	}
	if err = s.openSecret("mother:"+job.Mother.ID, motherSecret, &job.Mother.AccessToken); err != nil {
		return nil, err
	}
	if err = s.openSecret("proxy:"+job.Mother.ProxyID, proxySecret, &job.MotherProxy); err != nil {
		return nil, err
	}
	if err = s.openSecret(job.ID, accountSecret, &job.Account); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return job, nil
}
func (s *Store) RenewRefundLease(ctx context.Context, j *RefundJob) error {
	tag, err := s.Pool.Exec(ctx, "UPDATE refund_execution SET lease_until=now()+interval '90 seconds' WHERE refund_id=$1 AND lease_token=$2 AND lease_until>now() AND NOT released", j.ID, j.LeaseToken)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrState
	}
	return err
}

type Checkpoint struct {
	Diagnostic *RefundDiagnostic `json:"diagnostic,omitempty"`
	Baseline   *PersonalSnapshot `json:"baseline,omitempty"`
	Method     string            `json:"method"`
	TeamID     string            `json:"team_id"`
	UserID     string            `json:"user_id"`
}

// Every upstream mutation is preceded by a committed checkpoint. Accepted facts
// are written even if a lease expired, but cannot revive a released/other job.
func (s *Store) RefundCheckpoint(ctx context.Context, j *RefundJob, c Checkpoint) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", j.UserID); err != nil {
		return err
	}
	var step, token, status string
	var live, released bool
	err = tx.QueryRow(ctx, `SELECT e.step,e.lease_token,COALESCE(e.lease_until>now(),false),e.released,r.status FROM refunds r JOIN refund_execution e ON e.refund_id=r.id WHERE r.id=$1 FOR UPDATE OF r,e`, j.ID).Scan(&step, &token, &live, &released, &status)
	if err != nil {
		return err
	}
	if released || token != j.LeaseToken || (!live && c.Method != "merge_v2_accepted") || status == "succeeded" || status == "failed" {
		return ErrState
	}
	if c.Diagnostic != nil {
		if err = recordDiagnostic(ctx, tx, j.ID, *c.Diagnostic); err != nil {
			return err
		}
	}
	next, message := "", ""
	switch c.Method {
	case "personal_baseline":
		if step != "login" || c.Baseline == nil || !c.Baseline.Valid() {
			return ErrState
		}
		if _, err = tx.Exec(ctx, "UPDATE refund_execution SET personal_baseline=COALESCE(personal_baseline,$2::jsonb) WHERE refund_id=$1", j.ID, c.Baseline); err != nil {
			return err
		}
		return tx.Commit(ctx)
	case "verification_started":
		if step != "submitting" || j.RecoveryMode != "verify" {
			return ErrState
		}
		next = step
		message = "正在只读核实本次处理结果。"
	case "verification_result":
		if step != "submitting" || j.RecoveryMode != "verify" || c.Diagnostic == nil {
			return ErrState
		}
		if _, err = tx.Exec(ctx, "INSERT INTO refund_events(refund_id,message,internal) VALUES($1,'本轮只读核实结束，处理结果仍待确认。',true)", j.ID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	case "cleanup_started":
		if j.RecoveryMode != "cleanup" || oneOf(step, "submitting", "accepted", "removing", "completed", "failed") {
			return ErrState
		}
		next = step
		message = "正在清理未完成申请的本次资源。"
	case "cleanup_completed":
		if j.RecoveryMode != "cleanup" || oneOf(step, "submitting", "accepted", "removing", "completed", "failed") {
			return ErrState
		}
		var accepted bool
		if err = tx.QueryRow(ctx, "SELECT accepted_at IS NOT NULL FROM refund_execution WHERE refund_id=$1", j.ID).Scan(&accepted); err != nil {
			return err
		}
		if accepted {
			return ErrState
		}
		if _, err = tx.Exec(ctx, "INSERT INTO refund_events(refund_id,message,internal) VALUES($1,'本次资源已确认清理。',true)", j.ID); err != nil {
			return err
		}
		if err = transitionTx(ctx, tx, j.UserID, j.ID, "failed", "本次服务未完成，冻结服务费已释放。"); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "UPDATE refund_execution SET step='failed',released=true,lease_token='',lease_until=NULL,next_attempt_at=NULL,recovery_mode='',updated_at=now() WHERE refund_id=$1", j.ID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	case "inviting":
		if step != "assigned" && step != "inviting" {
			return ErrState
		}
		next = "inviting"
		message = "正在发送团队邀请。"
	case "invited":
		if step != "inviting" {
			return ErrState
		}
		next = "invited"
		message = "团队邀请已确认，准备登录账号。"
	case "login":
		if step != "invited" && step != "login" && step != "ready" {
			return ErrState
		}
		next = "login"
		message = "正在使用密码和 2FA 登录受邀团队。"
	case "merge_v2_ready":
		if (step != "login" && step != "ready") || c.TeamID != j.Mother.TeamID || strings.TrimSpace(c.UserID) == "" || len(c.UserID) > 150 {
			return ErrState
		}
		if _, err = tx.Exec(ctx, "UPDATE refund_execution SET child_user_id=$2 WHERE refund_id=$1", j.ID, c.UserID); err != nil {
			return err
		}
		next = "ready"
		message = "已确认团队会话，准备提交导出并停用个人空间。"
	case "merge_v2_submitting":
		if step != "ready" || c.TeamID != j.Mother.TeamID {
			return ErrState
		}
		next = "submitting"
		message = "正在提交导出并停用个人空间。"
	case "merge_v2_rejected":
		if step != "submitting" {
			return ErrState
		}
		next = "login"
		message = "停用请求未受理，保留账号并等待重新处理。"
	case "merge_v2_accepted":
		if step != "submitting" && step != "accepted" {
			return ErrState
		}
		next = "accepted"
		message = "导出停用已受理，准备移出团队。"
		if _, err = tx.Exec(ctx, "UPDATE refund_execution SET accepted_at=COALESCE(accepted_at,now()) WHERE refund_id=$1", j.ID); err != nil {
			return err
		}
	case "removing":
		if step != "accepted" && step != "removing" {
			return ErrState
		}
		next = "removing"
		message = "正在移出本次账号。"
	case "completed":
		if step != "removing" {
			return ErrState
		}
		next = "completed"
		message = "退款服务已完成"
		if err = transitionTx(ctx, tx, j.UserID, j.ID, "succeeded", message); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "UPDATE refund_execution SET completed_at=now(),released=true,lease_until=NULL,lease_token='',next_attempt_at=NULL,recovery_mode='' WHERE refund_id=$1", j.ID); err != nil {
			return err
		}
	default:
		return ErrInvalid
	}
	if _, err = tx.Exec(ctx, "UPDATE refund_execution SET step=$2,updated_at=now() WHERE refund_id=$1", j.ID, next); err != nil {
		return err
	}
	// A repeated start is a distinct attempt, even when the checkpoint does not
	// change. Retain its timestamp without changing recovery or upstream logic.
	if c.Method != "completed" && (next != step || c.Method == "login" || c.Method == "inviting" || c.Method == "removing" || c.Method == "verification_started" || c.Method == "cleanup_started") {
		if _, err = tx.Exec(ctx, "INSERT INTO refund_events(refund_id,message,internal) VALUES($1,$2,true)", j.ID, message); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) RefundNeedsAttention(ctx context.Context, j *RefundJob, message string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", j.UserID); err != nil {
		return err
	}
	var token, step string
	var released bool
	if err = tx.QueryRow(ctx, "SELECT lease_token,step,released FROM refund_execution WHERE refund_id=$1 FOR UPDATE", j.ID).Scan(&token, &step, &released); err != nil {
		return err
	}
	if token != j.LeaseToken || released {
		return nil
	}
	if step == "submitting" {
		message = "停用提交结果待核实，未重发请求，也未移出团队。"
	}
	if _, err = tx.Exec(ctx, "INSERT INTO refund_events(refund_id,message,internal) VALUES($1,$2,true)", j.ID, message); err != nil {
		return err
	}
	if err = transitionTx(ctx, tx, j.UserID, j.ID, "attention", "申请需要跟进，请联系服务专员。"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE refund_execution SET lease_until=NULL,lease_token='',updated_at=now() WHERE refund_id=$1", j.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ResumeRefund(ctx context.Context, actor, id, action, note string) error {
	note = strings.TrimSpace(note)
	if len(note) < 5 || len(note) > 500 {
		return invalid("请填写处理原因（5～500字节）")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = adminLock(ctx, tx, actor); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(718374023)"); err != nil {
		return err
	}
	var uid string
	if err = tx.QueryRow(ctx, "SELECT user_id FROM refunds WHERE id=$1", id).Scan(&uid); err != nil {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", uid); err != nil {
		return err
	}
	var step, status, child, recoveryMode string
	var live, accepted bool
	err = tx.QueryRow(ctx, `SELECT e.step,r.status,COALESCE(e.lease_until>now(),false),e.accepted_at IS NOT NULL,e.child_user_id,e.recovery_mode FROM refunds r JOIN refund_execution e ON e.refund_id=r.id WHERE r.id=$1 FOR UPDATE OF r,e`, id).Scan(&step, &status, &live, &accepted, &child, &recoveryMode)
	if err != nil {
		return ErrNotFound
	}
	if live || (status != "attention" && !(oneOf(status, "processing", "verifying") && recoveryMode != "")) {
		return invalid("只能处理已停止的待跟进申请")
	}
	message := "管理员已安排续跑，将从保留的进度继续。"
	switch action {
	case "resume":
		if step == "submitting" {
			return invalid("处理结果未知，请先核实本次结果")
		}
	case "verify_only":
		var enabled bool
		if err = tx.QueryRow(ctx, "SELECT (recovery->>'enabled')::boolean FROM refund_settings WHERE id").Scan(&enabled); err != nil {
			return err
		}
		if !enabled {
			return invalid("请先开启自动恢复与核实")
		}
		if step != "submitting" {
			return ErrState
		}
		if _, err = tx.Exec(ctx, "UPDATE refund_execution SET recovery_mode='verify',verify_count=0,next_attempt_at=now() WHERE refund_id=$1", id); err != nil {
			return err
		}
		message = "管理员已安排只读核实，不重复提交。"
	case "confirm_accepted":
		if step != "submitting" || child == "" {
			return ErrState
		}
		step = "accepted"
		if _, err = tx.Exec(ctx, "UPDATE refund_execution SET accepted_at=COALESCE(accepted_at,now()) WHERE refund_id=$1", id); err != nil {
			return err
		}
		message = "管理员已核实停用受理，将继续移出团队。"
	case "confirm_rejected":
		if step != "submitting" || accepted {
			return ErrState
		}
		step = "login"
		message = "管理员已核实请求未受理，将重新登录后提交。"
	case "fail_cleaned":
		if accepted || step == "submitting" {
			return invalid("本次处理尚未核实，不能直接结束失败")
		}
		if err = transitionTx(ctx, tx, uid, id, "failed", "本次服务未完成，冻结服务费已释放。"); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "UPDATE refund_execution SET step='failed',released=true,lease_token='',lease_until=NULL,next_attempt_at=NULL,recovery_mode='',updated_at=now() WHERE refund_id=$1", id); err != nil {
			return err
		}
	default:
		return ErrInvalid
	}
	if action != "fail_cleaned" {
		if action != "verify_only" {
			if _, err = tx.Exec(ctx, "UPDATE refund_execution SET recovery_mode='',next_attempt_at=NULL,retry_count=0,cleanup_count=0,verify_count=0,recovery_started_at=NULL WHERE refund_id=$1", id); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, "INSERT INTO refund_events(refund_id,message,internal) VALUES($1,$2,true)", id, message); err != nil {
			return err
		}
		if err = transitionTx(ctx, tx, uid, id, "processing", "正在继续处理"); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "UPDATE refund_execution SET step=$2,lease_token='',lease_until=NULL,updated_at=now() WHERE refund_id=$1", id, step); err != nil {
			return err
		}
	}
	if err = audit(ctx, tx, actor, "refund."+action, id, map[string]any{"reason": note}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Public progress is intentionally independent of the private protocol steps.
func (s *Store) RefundProgress(ctx context.Context, j *RefundJob) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var uid string
	if err = tx.QueryRow(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", j.UserID).Scan(&uid); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE refund_execution e SET progress_at=now() FROM refunds r WHERE e.refund_id=$1 AND e.lease_token=$2 AND e.lease_until>now() AND NOT e.released AND e.progress_at<=now()-interval '60 seconds' AND r.id=e.refund_id AND r.status IN ('processing','verifying')`, j.ID, j.LeaseToken)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if _, err = tx.Exec(ctx, "INSERT INTO refund_events(refund_id,message) VALUES($1,'处理时间延长，正在继续')", j.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE refunds SET message='处理时间延长，正在继续',updated_at=now() WHERE id=$1", j.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

func Permissions(role string) []string {
	base := []string{"workspace.read", "refunds.create", "refunds.cancel", "billing.read", "keys.manage", "codes.redeem"}
	if role == "admin" {
		return append(base, "admin.overview.read", "admin.reports.read", "admin.refunds.read", "admin.users.manage", "admin.credits.write", "admin.codes.manage", "admin.audit.read", "admin.refund.execute", "admin.announcements.manage")
	}
	if role == "operator" {
		return append(base, "admin.overview.read", "admin.refunds.read")
	}
	return base
}
func (u User) Can(permission string) bool {
	if u.Disabled {
		return false
	}
	for _, p := range Permissions(u.Role) {
		if p == permission {
			return true
		}
	}
	return false
}
func invalid(message string) error { return fmt.Errorf("%w：%s", ErrInvalid, message) }
func audit(ctx context.Context, tx pgx.Tx, actor, action, target string, metadata any) error {
	raw, e := json.Marshal(metadata)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, "INSERT INTO audit_logs(actor_id,action,target_id,metadata) VALUES($1,$2,$3,$4)", actor, action, target, raw)
	return e
}

// Recheck administrative authority within the transaction, serializing role changes.
func adminLock(ctx context.Context, tx pgx.Tx, actor string) error {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(718374022)"); err != nil {
		return err
	}
	var role string
	var disabled bool
	if err := tx.QueryRow(ctx, "SELECT role,disabled FROM users WHERE id=$1", actor).Scan(&role, &disabled); err != nil {
		return err
	}
	if role != "admin" || disabled {
		return ErrForbidden
	}
	return nil
}

type UserInput struct {
	Email      string `json:"email"`
	Name       string `json:"name"`
	Password   string `json:"password"`
	Role       string `json:"role"`
	ServiceFee int64  `json:"service_fee"`
	Disabled   bool   `json:"disabled"`
}

func validateUser(in UserInput, create bool) error {
	if in.Role != "admin" && in.Role != "operator" && in.Role != "member" {
		return invalid("角色无效")
	}
	if len(strings.TrimSpace(in.Name)) < 1 || len(in.Name) > 80 {
		return invalid("姓名须为 1～80 字节")
	}
	if in.ServiceFee < 0 || in.ServiceFee > 10000000 {
		return invalid("服务费超出允许范围")
	}
	if create {
		address, err := mail.ParseAddress(in.Email)
		if err != nil || address.Address != in.Email || len(in.Email) > 254 {
			return invalid("邮箱格式不正确")
		}
		if len(in.Password) < 12 || len(in.Password) > 72 {
			return invalid("密码须为 12～72 字节")
		}
	}
	return nil
}
func (s *Store) AdminUsers(ctx context.Context, q string, page, size int) ([]User, int, error) {
	where := " FROM users WHERE ($1='' OR position(lower($1) in lower(email||' '||name))>0)"
	var total int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*)"+where, q).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.Pool.Query(ctx, "SELECT "+userColumns+where+" ORDER BY created_at DESC,id LIMIT $2 OFFSET $3", q, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, e := scanUser(rows)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, u)
	}
	return out, total, rows.Err()
}
func (s *Store) AdminCreateUser(ctx context.Context, actor string, in UserInput) (string, error) {
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.Name = strings.TrimSpace(in.Name)
	if err := validateUser(in, true); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if err = adminLock(ctx, tx, actor); err != nil {
		return "", err
	}
	id := ID()
	_, err = tx.Exec(ctx, "INSERT INTO users(id,email,name,password_hash,role,service_fee,disabled) VALUES($1,$2,$3,$4,$5,$6,$7)", id, in.Email, in.Name, string(hash), in.Role, in.ServiceFee, in.Disabled)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return "", invalid("邮箱已存在")
		}
		return "", err
	}
	if err = audit(ctx, tx, actor, "user.create", id, map[string]any{"email": in.Email, "role": in.Role, "service_fee": in.ServiceFee}); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}
func (s *Store) AdminUpdateUser(ctx context.Context, actor, id string, in UserInput) error {
	if err := validateUser(in, false); err != nil {
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
	before, err := scanUser(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM users WHERE id=$1 FOR UPDATE", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if id == actor && (in.Role != "admin" || in.Disabled) {
		return invalid("不能停用自己或降低自己的管理权限")
	}
	if before.Role == "admin" && !before.Disabled && (in.Role != "admin" || in.Disabled) {
		var n int
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM users WHERE role='admin' AND NOT disabled").Scan(&n); err != nil {
			return err
		}
		if n <= 1 {
			return invalid("至少保留一名启用的管理员")
		}
	}
	_, err = tx.Exec(ctx, "UPDATE users SET name=$2,role=$3,service_fee=$4,disabled=$5 WHERE id=$1", id, strings.TrimSpace(in.Name), in.Role, in.ServiceFee, in.Disabled)
	if err != nil {
		return err
	}
	if in.Disabled || in.Role != before.Role {
		if _, err = tx.Exec(ctx, "DELETE FROM sessions WHERE user_id=$1", id); err != nil {
			return err
		}
	}
	if err = audit(ctx, tx, actor, "user.update", id, map[string]any{"before": map[string]any{"name": before.Name, "role": before.Role, "service_fee": before.ServiceFee, "disabled": before.Disabled}, "after": map[string]any{"name": in.Name, "role": in.Role, "service_fee": in.ServiceFee, "disabled": in.Disabled}}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) AdminPassword(ctx context.Context, actor, id, password string) error {
	if len(password) < 12 || len(password) > 72 {
		return invalid("密码须为 12～72 字节")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
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
	tag, err := tx.Exec(ctx, "UPDATE users SET password_hash=$2 WHERE id=$1", id, string(hash))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, "DELETE FROM sessions WHERE user_id=$1", id); err != nil {
		return err
	}
	if err = audit(ctx, tx, actor, "user.password_reset", id, map[string]bool{"sessions_revoked": true}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) AdminCredit(ctx context.Context, actor, id, key string, amount int64, note string) error {
	if amount == 0 || amount < -100000000 || amount > 100000000 || len(note) > 500 || strings.TrimSpace(note) == "" {
		return invalid("调整积分须非零，单次增加或减少不超过 1,000,000，并填写调整备注")
	}
	if len(key) < 8 || len(key) > 128 {
		return invalid("请提供有效幂等键")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = adminLock(ctx, tx, actor); err != nil {
		return err
	}
	raw, _ := json.Marshal([]any{id, amount, note})
	hash := Hash(string(raw))
	var old string
	err = tx.QueryRow(ctx, "SELECT request_hash FROM admin_idempotency WHERE actor_id=$1 AND key=$2", actor, key).Scan(&old)
	if err == nil {
		if old != hash {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var balance, frozen int64
	err = tx.QueryRow(ctx, "SELECT balance,frozen FROM users WHERE id=$1 FOR UPDATE", id).Scan(&balance, &frozen)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if amount < 0 && balance-frozen < -amount {
		return invalid("减少积分不能超过当前可用积分，已预留积分不能扣减")
	}
	if _, err = tx.Exec(ctx, "UPDATE users SET balance=balance+$2 WHERE id=$1", id, amount); err != nil {
		return err
	}
	balance += amount
	kind := "credit"
	if amount < 0 {
		kind = "debit"
	}
	if _, err = tx.Exec(ctx, "INSERT INTO ledger(user_id,kind,amount,balance_after,note) VALUES($1,$2,$3,$4,$5)", id, kind, amount, balance, note); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO admin_idempotency(actor_id,key,request_hash) VALUES($1,$2,$3)", actor, key, hash); err != nil {
		return err
	}
	if err = audit(ctx, tx, actor, "balance."+kind, id, map[string]any{"amount": amount, "note": note, "balance_after": balance}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type AdminRefund struct {
	Refund
	UserID    string `json:"user_id"`
	UserName  string `json:"user_name"`
	UserEmail string `json:"user_email"`
}

func (s *Store) AdminRefunds(ctx context.Context, q, status string, page, size int) ([]AdminRefund, int, error) {
	where := " FROM refunds r JOIN users u ON u.id=r.user_id WHERE ($1='' OR position(lower($1) in lower(r.email||' '||u.email||' '||r.id))>0) AND ($2='' OR r.status=$2)"
	var total int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*)"+where, q, status).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.Pool.Query(ctx, "SELECT r.id,r.email,r.status,r.fee,r.fee_status,r.message,r.created_at,r.updated_at,u.id,u.name,u.email"+where+" ORDER BY r.created_at DESC,r.id LIMIT $3 OFFSET $4", q, status, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []AdminRefund{}
	for rows.Next() {
		var a AdminRefund
		if err = rows.Scan(&a.ID, &a.Email, &a.Status, &a.Fee, &a.FeeStatus, &a.Message, &a.CreatedAt, &a.UpdatedAt, &a.UserID, &a.UserName, &a.UserEmail); err != nil {
			return nil, 0, err
		}
		a.Message = publicRefundMessage(a.Status, a.Message)
		out = append(out, a)
	}
	return out, total, rows.Err()
}
func (s *Store) RefundOwner(ctx context.Context, id string) (string, error) {
	var uid string
	err := s.Pool.QueryRow(ctx, "SELECT user_id FROM refunds WHERE id=$1", id).Scan(&uid)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return uid, err
}

type Audit struct {
	ID        int64           `json:"id"`
	Actor     string          `json:"actor"`
	Action    string          `json:"action"`
	Target    string          `json:"target"`
	Metadata  json.RawMessage `json:"metadata"`
	CreatedAt time.Time       `json:"created_at"`
}

func (s *Store) Audits(ctx context.Context, page, size int) ([]Audit, int, error) {
	var total int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM audit_logs").Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.Pool.Query(ctx, "SELECT a.id,u.email,a.action,a.target_id,a.metadata,a.created_at FROM audit_logs a JOIN users u ON u.id=a.actor_id ORDER BY a.id DESC LIMIT $1 OFFSET $2", size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Audit{}
	for rows.Next() {
		var a Audit
		if err = rows.Scan(&a.ID, &a.Actor, &a.Action, &a.Target, &a.Metadata, &a.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}
func (s *Store) AdminOverview(ctx context.Context, financial bool) (map[string]any, error) {
	out := map[string]any{"provider_ready": s.ProviderReady(ctx)}
	var users, refunds, active, done int
	err := s.Pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM users),(SELECT count(*) FROM refunds),(SELECT count(*) FROM refunds WHERE status IN ('queued','processing','verifying','attention')),(SELECT count(*) FROM refunds WHERE status='succeeded')").Scan(&users, &refunds, &active, &done)
	if err != nil {
		return nil, err
	}
	out["users"] = users
	out["refunds"] = refunds
	out["active"] = active
	out["succeeded"] = done
	if financial {
		var balance, frozen, revenue int64
		var codes int
		err = s.Pool.QueryRow(ctx, "SELECT COALESCE(sum(balance),0),COALESCE(sum(frozen),0),(SELECT COALESCE(-sum(amount),0) FROM ledger WHERE kind='settlement'),(SELECT count(*) FROM redeem_codes) FROM users").Scan(&balance, &frozen, &revenue, &codes)
		if err != nil {
			return nil, err
		}
		out["balance"] = balance
		out["frozen"] = frozen
		out["revenue"] = revenue
		out["codes"] = codes
	}
	return out, nil
}

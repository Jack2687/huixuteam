package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Archive identity instead of cascading through immutable financial records.
// The user row lock also serializes deletion with new refund submissions.
func (s *Store) AdminDeleteUser(ctx context.Context, actor, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = adminLock(ctx, tx, actor); err != nil {
		return err
	}
	if actor == id {
		return invalid("不能删除当前登录的管理员账户")
	}
	var email, name, role string
	var balance, frozen int64
	var disabled bool
	var deletedAt *time.Time
	err = tx.QueryRow(ctx, "SELECT email,name,role,balance,frozen,disabled,deleted_at FROM users WHERE id=$1 FOR UPDATE", id).Scan(&email, &name, &role, &balance, &frozen, &disabled, &deletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if deletedAt != nil {
		return nil
	}
	if role == "admin" && !disabled {
		var n int
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM users WHERE role='admin' AND NOT disabled AND deleted_at IS NULL").Scan(&n); err != nil {
			return err
		}
		if n <= 1 {
			return invalid("至少保留一名启用的管理员")
		}
	}
	if frozen != 0 {
		return invalid("该账户仍有已预留积分，请先处理相关申请")
	}
	var active bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM refunds WHERE user_id=$1 AND (status IN ('queued','processing','verifying','attention') OR fee_status='frozen'))", id).Scan(&active); err != nil {
		return err
	}
	if active {
		return invalid("该账户仍有未结束的申请，暂不能删除")
	}
	if _, err = tx.Exec(ctx, "UPDATE users SET deleted_at=now(),disabled=true WHERE id=$1", id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "DELETE FROM sessions WHERE user_id=$1", id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE api_keys SET revoked_at=COALESCE(revoked_at,now()) WHERE user_id=$1", id); err != nil {
		return err
	}
	if err = audit(ctx, tx, actor, "user.delete", id, map[string]any{"email": email, "name": name, "role": role, "balance": balance, "history_retained": true}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

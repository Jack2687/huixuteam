package store

import (
	"context"
	"strings"
)

// AdminSetServiceFees changes only the selected accounts' price for new requests.
// Locking user rows also serializes this change with fee reservation on submission.
func (s *Store) AdminSetServiceFees(ctx context.Context, actor string, ids []string, fee int64) error {
	if len(ids) == 0 || len(ids) > 1000 {
		return invalid("请选择 1～1000 个账户")
	}
	if fee < 0 || fee > 10000000 {
		return invalid("服务费超出允许范围")
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || strings.TrimSpace(id) != id || len(id) > 128 || seen[id] {
			return invalid("账户选择无效或重复，请重新选择")
		}
		seen[id] = true
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = adminLock(ctx, tx, actor); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, "SELECT id,service_fee FROM users WHERE id=ANY($1) AND deleted_at IS NULL ORDER BY id FOR UPDATE", ids)
	if err != nil {
		return err
	}
	type price struct {
		id  string
		fee int64
	}
	before := make([]price, 0, len(ids))
	for rows.Next() {
		var p price
		if err = rows.Scan(&p.id, &p.fee); err != nil {
			rows.Close()
			return err
		}
		before = append(before, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(before) != len(ids) {
		return invalid("部分账户已删除或不存在，本次未作调整，请刷新并重新选择")
	}
	if _, err = tx.Exec(ctx, "UPDATE users SET service_fee=$2 WHERE id=ANY($1) AND deleted_at IS NULL AND service_fee<>$2", ids, fee); err != nil {
		return err
	}
	batchID := ID()
	for _, p := range before {
		if p.fee == fee {
			continue
		}
		if err = audit(ctx, tx, actor, "user.service_fee", p.id, map[string]any{
			"batch_id": batchID, "before": p.fee, "after": fee,
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

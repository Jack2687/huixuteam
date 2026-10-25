package store

import (
	"context"
	"strings"
)

func validateCodeIDs(ids []string) error {
	if len(ids) == 0 || len(ids) > 1000 {
		return invalid("请选择 1～1000 个兑换码")
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) == "" || len(id) > 128 || seen[id] {
			return invalid("兑换码选择无效或重复，请刷新后重试")
		}
		seen[id] = true
	}
	return nil
}

// ExportCodes decrypts only explicitly selected codes after rechecking authority.
// Hash-only legacy codes cannot be reconstructed; never silently export a subset.
func (s *Store) ExportCodes(ctx context.Context, actor string, ids []string) ([]string, error) {
	if err := validateCodeIDs(ids); err != nil {
		return nil, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = adminLock(ctx, tx, actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, "SELECT id,code_secret,code_hash FROM redeem_codes WHERE id=ANY($1) AND deleted_at IS NULL ORDER BY id FOR SHARE", ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]string, len(ids))
	for rows.Next() {
		var id, hash, code string
		var secret []byte
		if err = rows.Scan(&id, &secret, &hash); err != nil {
			rows.Close()
			return nil, err
		}
		if len(secret) == 0 {
			rows.Close()
			return nil, invalid("所选包含未保存完整码的历史兑换码，无法导出；请取消选择这些历史码")
		}
		if err = s.openSecret("redeem-code:"+id, secret, &code); err != nil || Hash(code) != hash {
			rows.Close()
			return nil, invalid("兑换码读取失败，请联系维护人员")
		}
		byID[id] = code
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(byID) != len(ids) {
		return nil, invalid("部分兑换码已删除或不存在，请刷新后重试")
	}
	codes := make([]string, 0, len(ids))
	for _, id := range ids {
		codes = append(codes, byID[id])
	}
	if err = audit(ctx, tx, actor, "code.export", "batch", map[string]any{"ids": ids, "count": len(ids)}); err != nil {
		return nil, err
	}
	return codes, tx.Commit(ctx)
}

// Remove codes from use and management while preserving redemption/ledger history.
// Repeated deletion is safe; deleted codes cannot be re-enabled or exported.
func (s *Store) DeleteCodes(ctx context.Context, actor string, ids []string) error {
	if err := validateCodeIDs(ids); err != nil {
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
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM redeem_codes WHERE id=ANY($1)", ids).Scan(&count); err != nil {
		return err
	}
	if count != len(ids) {
		return invalid("部分兑换码不存在，请刷新后重试")
	}
	tag, err := tx.Exec(ctx, "UPDATE redeem_codes SET deleted_at=now(),disabled=true,code_secret=NULL WHERE id=ANY($1) AND deleted_at IS NULL", ids)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		if err = audit(ctx, tx, actor, "code.delete", "batch", map[string]any{"ids": ids, "count": tag.RowsAffected()}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

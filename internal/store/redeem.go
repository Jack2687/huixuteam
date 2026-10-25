package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

type RedeemCode struct {
	ID         string     `json:"id"`
	Prefix     string     `json:"prefix"`
	Amount     int64      `json:"amount"`
	MaxUses    int        `json:"max_uses"`
	UsedCount  int        `json:"used_count"`
	ExpiresAt  *time.Time `json:"expires_at"`
	Disabled   bool       `json:"disabled"`
	Note       string     `json:"note"`
	CreatedAt  time.Time  `json:"created_at"`
	Exportable bool       `json:"exportable"`
}
type CodeInput struct {
	Count     int        `json:"count"`
	Amount    int64      `json:"amount"`
	MaxUses   int        `json:"max_uses"`
	ExpiresAt *time.Time `json:"expires_at"`
	Note      string     `json:"note"`
}

func (s *Store) GenerateCodes(ctx context.Context, actor string, in CodeInput) ([]string, error) {
	if in.Count < 1 || in.Count > 100 || in.Amount < 1 || in.Amount > 100000000 || in.MaxUses < 1 || in.MaxUses > 10000 || len(in.Note) > 200 {
		return nil, invalid("批量数量 1～100，积分大于 0 且不超过 1,000,000，使用次数 1～10000，备注不超过 200 字节")
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now()) {
		return nil, invalid("有效期必须晚于当前时间")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = adminLock(ctx, tx, actor); err != nil {
		return nil, err
	}
	codes := make([]string, 0, in.Count)
	for i := 0; i < in.Count; i++ {
		code := "HUIXU-" + strings.ToUpper(ID())
		id := ID()
		secret, e := s.sealSecret("redeem-code:"+id, code)
		if e != nil {
			return nil, e
		}
		_, err = tx.Exec(ctx, "INSERT INTO redeem_codes(id,code_hash,prefix,amount,max_uses,expires_at,note,created_by,code_secret) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)", id, Hash(code), code[:14], in.Amount, in.MaxUses, in.ExpiresAt, in.Note, actor, secret)
		if err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	if err = audit(ctx, tx, actor, "code.generate", "batch", map[string]any{"count": in.Count, "amount": in.Amount, "max_uses": in.MaxUses, "expires_at": in.ExpiresAt, "note": in.Note}); err != nil {
		return nil, err
	}
	return codes, tx.Commit(ctx)
}
func (s *Store) Codes(ctx context.Context, page, size int) ([]RedeemCode, int, error) {
	var total int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM redeem_codes WHERE deleted_at IS NULL").Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.Pool.Query(ctx, "SELECT id,prefix,amount,max_uses,used_count,expires_at,disabled,note,created_at,code_secret IS NOT NULL FROM redeem_codes WHERE deleted_at IS NULL ORDER BY created_at DESC,id LIMIT $1 OFFSET $2", size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []RedeemCode{}
	for rows.Next() {
		var c RedeemCode
		if err = rows.Scan(&c.ID, &c.Prefix, &c.Amount, &c.MaxUses, &c.UsedCount, &c.ExpiresAt, &c.Disabled, &c.Note, &c.CreatedAt, &c.Exportable); err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}
func (s *Store) DisableCode(ctx context.Context, actor, id string, disabled bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = adminLock(ctx, tx, actor); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "UPDATE redeem_codes SET disabled=$2 WHERE id=$1 AND deleted_at IS NULL", id, disabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = audit(ctx, tx, actor, "code.update", id, map[string]bool{"disabled": disabled}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) Redeem(ctx context.Context, uid, code string) (int64, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) < 10 || len(code) > 100 {
		return 0, invalid("兑换码无效")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var balance int64
	if err = tx.QueryRow(ctx, "SELECT balance FROM users WHERE id=$1 AND NOT disabled FOR UPDATE", uid).Scan(&balance); err != nil {
		return 0, err
	}
	var id, prefix string
	var amount int64
	var max, used int
	var expiry *time.Time
	var disabled bool
	err = tx.QueryRow(ctx, "SELECT id,prefix,amount,max_uses,used_count,expires_at,disabled FROM redeem_codes WHERE code_hash=$1 AND deleted_at IS NULL FOR UPDATE", Hash(code)).Scan(&id, &prefix, &amount, &max, &used, &expiry, &disabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, invalid("兑换码不存在或不可用")
	}
	if err != nil {
		return 0, err
	}
	var redeemed bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM redemptions WHERE user_id=$1 AND code_id=$2)", uid, id).Scan(&redeemed); err != nil {
		return 0, err
	}
	if redeemed {
		return 0, invalid("你已使用过此兑换码，请勿重复兑换")
	}
	if disabled || used >= max || (expiry != nil && !expiry.After(time.Now())) {
		return 0, invalid("兑换码已停用、过期或使用完毕")
	}
	if _, err = tx.Exec(ctx, "UPDATE users SET balance=balance+$2 WHERE id=$1", uid, amount); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, "UPDATE redeem_codes SET used_count=used_count+1 WHERE id=$1", id); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO redemptions(code_id,user_id,amount) VALUES($1,$2,$3)", id, uid, amount); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO ledger(user_id,kind,amount,balance_after,note) VALUES($1,'redeem',$2,$3,$4)", uid, amount, balance+amount, "兑换码 "+prefix+"••••"); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO notifications(user_id,message) VALUES($1,'兑换成功，积分已存入可用余额。')", uid); err != nil {
		return 0, err
	}
	if err = audit(ctx, tx, uid, "code.redeem", id, map[string]int64{"amount": amount}); err != nil {
		return 0, err
	}
	return amount, tx.Commit(ctx)
}

type Redemption struct {
	ID        int64     `json:"id"`
	Prefix    string    `json:"prefix"`
	Email     string    `json:"email"`
	Amount    int64     `json:"amount"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) Redemptions(ctx context.Context, page, size int) ([]Redemption, int, error) {
	var total int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM redemptions").Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.Pool.Query(ctx, "SELECT r.id,c.prefix,u.email,r.amount,r.created_at FROM redemptions r JOIN redeem_codes c ON c.id=r.code_id JOIN users u ON u.id=r.user_id ORDER BY r.id DESC LIMIT $1 OFFSET $2", size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Redemption{}
	for rows.Next() {
		var r Redemption
		if err = rows.Scan(&r.ID, &r.Prefix, &r.Email, &r.Amount, &r.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

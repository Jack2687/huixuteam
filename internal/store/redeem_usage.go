package store

import (
	"context"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
	"unicode/utf8"
)

type RedeemUsageItem struct {
	ID         int64     `json:"id"`
	CodeID     string    `json:"code_id"`
	Prefix     string    `json:"prefix"`
	UserID     string    `json:"user_id"`
	Email      string    `json:"email"`
	Name       string    `json:"name"`
	Note       string    `json:"note"`
	Amount     int64     `json:"amount"`
	RedeemedAt time.Time `json:"redeemed_at"`
}
type RedeemUsageSummary struct {
	Redemptions int   `json:"redemptions"`
	Codes       int   `json:"codes"`
	Accounts    int   `json:"accounts"`
	Amount      int64 `json:"amount"`
}
type RedeemUsageReport struct {
	Items    []RedeemUsageItem  `json:"items"`
	Summary  RedeemUsageSummary `json:"summary"`
	Total    int                `json:"total"`
	Page     int                `json:"page"`
	Size     int                `json:"size"`
	Start    string             `json:"start_date"`
	End      string             `json:"end_date"`
	Timezone string             `json:"timezone"`
}

func (s *Store) RedeemUsage(ctx context.Context, period, anchor, query string, page, size int) (RedeemUsageReport, error) {
	result := RedeemUsageReport{Items: []RedeemUsageItem{}, Page: page, Size: size, Timezone: "Asia/Shanghai"}
	if page < 1 || page > 100000 || size < 1 || size > 100 || utf8.RuneCountInString(query) > 200 {
		return result, invalid("分页或搜索条件无效")
	}
	start, end, err := AccountReportPeriod(period, anchor, time.Now())
	if err != nil {
		return result, err
	}
	result.Start, result.End = start.Format("2006-01-02"), end.AddDate(0, 0, -1).Format("2006-01-02")
	pattern := "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(strings.TrimSpace(query)) + "%"
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SET LOCAL statement_timeout='10s'"); err != nil {
		return result, err
	}
	where := ` FROM redemptions r JOIN redeem_codes c ON c.id=r.code_id JOIN users u ON u.id=r.user_id
 WHERE r.created_at >= $1 AND r.created_at < $2 AND (c.code_hash=$4 OR c.prefix ILIKE $3 OR c.note ILIKE $3 OR u.email ILIKE $3 OR u.name ILIKE $3)`
	codeHash := Hash(strings.ToUpper(strings.TrimSpace(query)))
	err = tx.QueryRow(ctx, "SELECT count(*),count(DISTINCT r.code_id),count(DISTINCT r.user_id),COALESCE(sum(r.amount),0)"+where, start, end, pattern, codeHash).Scan(&result.Summary.Redemptions, &result.Summary.Codes, &result.Summary.Accounts, &result.Summary.Amount)
	if err != nil {
		return result, err
	}
	result.Total = result.Summary.Redemptions
	rows, err := tx.Query(ctx, "SELECT r.id,r.code_id,c.prefix,r.user_id,u.email,u.name,c.note,r.amount,r.created_at"+where+" ORDER BY r.created_at DESC,r.id DESC LIMIT $5 OFFSET $6", start, end, pattern, codeHash, size, (page-1)*size)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var item RedeemUsageItem
		if err = rows.Scan(&item.ID, &item.CodeID, &item.Prefix, &item.UserID, &item.Email, &item.Name, &item.Note, &item.Amount, &item.RedeemedAt); err != nil {
			rows.Close()
			return result, err
		}
		result.Items = append(result.Items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

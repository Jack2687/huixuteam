package store

import "context"

type SiteServiceSettings struct {
	DefaultServiceFee int64 `json:"default_service_fee"`
}

func (s *Store) SiteService(ctx context.Context) (SiteServiceSettings, error) {
	var v SiteServiceSettings
	err := s.Pool.QueryRow(ctx, "SELECT default_service_fee FROM site_service_settings WHERE id").Scan(&v.DefaultServiceFee)
	return v, err
}

func (s *Store) SaveSiteService(ctx context.Context, actor string, v SiteServiceSettings) error {
	if v.DefaultServiceFee < 0 || v.DefaultServiceFee > 10000000 {
		return invalid("注册默认服务单价须为0～100000积分")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = adminLock(ctx, tx, actor); err != nil {
		return err
	}
	var before int64
	if err = tx.QueryRow(ctx, "SELECT default_service_fee FROM site_service_settings WHERE id FOR UPDATE").Scan(&before); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE site_service_settings SET default_service_fee=$1,updated_at=now() WHERE id", v.DefaultServiceFee); err != nil {
		return err
	}
	if err = audit(ctx, tx, actor, "site.service", "site", map[string]any{"before": before, "after": v.DefaultServiceFee}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

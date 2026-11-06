package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"math"
	"math/big"
	"time"
)

func (s *Store) SliderLimit(ctx context.Context, ip string) error {
	var hits int
	e := s.Pool.QueryRow(ctx, `INSERT INTO auth_email_limits(key) VALUES($1) ON CONFLICT(key) DO UPDATE SET
 hits=CASE WHEN auth_email_limits.window_start<now()-interval '1 minute' THEN 1 ELSE auth_email_limits.hits+1 END,
 window_start=CASE WHEN auth_email_limits.window_start<now()-interval '1 minute' THEN now() ELSE auth_email_limits.window_start END RETURNING hits`, "slider:"+Hash(ip)).Scan(&hits)
	if e != nil {
		return e
	}
	if hits > 90 {
		return ErrAuthRate
	}
	return nil
}
func (s *Store) LoginSlider(ctx context.Context, binding string) (map[string]any, error) {
	cfg, e := s.SiteSettings(ctx)
	if e != nil {
		return nil, e
	}
	if !cfg.Security.LoginSliderEnabled {
		return map[string]any{"enabled": false}, nil
	}
	n, e := rand.Int(rand.Reader, big.NewInt(171))
	if e != nil {
		return nil, e
	}
	x := int(n.Int64()) + 90
	id := Token()
	if _, e = s.Pool.Exec(ctx, "DELETE FROM auth_slider_challenges WHERE expires_at<now()"); e != nil {
		return nil, e
	}
	if _, e = s.Pool.Exec(ctx, "INSERT INTO auth_slider_challenges(id,binding,target,expires_at) VALUES($1,$2,$3,now()+interval '2 minutes')", id, Hash(binding), x); e != nil {
		return nil, e
	}
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="320" height="90"><rect width="320" height="90" rx="10" fill="#edf5f2"/><path d="M0 68 Q80 0 160 66 T320 40" fill="none" stroke="#bdd7ce" stroke-width="12"/><rect x="%d" y="25" width="36" height="40" rx="7" fill="#315f50" stroke="#174a3b" stroke-dasharray="4 3"/></svg>`, x)
	return map[string]any{"enabled": true, "id": id, "image": "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))}, nil
}
func (s *Store) VerifyLoginSlider(ctx context.Context, binding, id string, x float64) (map[string]any, error) {
	if len(id) > 80 || x < 0 || x > 284 || math.IsNaN(x) || math.IsInf(x, 0) {
		return nil, invalid("请重新完成滑动验证")
	}
	var target int
	var created, expires time.Time
	e := s.Pool.QueryRow(ctx, "DELETE FROM auth_slider_challenges WHERE id=$1 AND binding=$2 AND proof_hash IS NULL RETURNING target,created_at,expires_at", id, Hash(binding)).Scan(&target, &created, &expires)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	if e != nil || time.Now().After(expires) || time.Since(created) < 300*time.Millisecond || math.Abs(x-float64(target)) > 5 {
		return nil, invalid("滑动位置不正确或验证已过期，请重试")
	}
	proof := Token()
	_, e = s.Pool.Exec(ctx, "INSERT INTO auth_slider_challenges(id,binding,target,proof_hash,expires_at) VALUES($1,$2,0,$3,now()+interval '1 minute')", id, Hash(binding), Hash(proof))
	return map[string]any{"proof": proof, "expires_in": 60}, e
}
func (s *Store) ConsumeLoginSlider(ctx context.Context, binding, proof string) error {
	cfg, e := s.SiteSettings(ctx)
	if e != nil {
		return e
	}
	if !cfg.Security.LoginSliderEnabled {
		return nil
	}
	if len(proof) == 0 || len(proof) > 128 {
		return invalid("请先完成滑动验证")
	}
	tag, e := s.Pool.Exec(ctx, "DELETE FROM auth_slider_challenges WHERE binding=$1 AND proof_hash=$2 AND expires_at>now()", Hash(binding), Hash(proof))
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return invalid("滑动验证已失效，请重新验证")
	}
	return nil
}

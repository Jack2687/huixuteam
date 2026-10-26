package store

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

type RiskIdentity struct {
	Type   string
	ID     string
	Label  string
	UserID string
	KeyID  string
}
type RiskConsumption struct {
	Rule     string
	Policy   RiskRule
	Identity RiskIdentity
}
type RiskDecision struct {
	Rule         string `json:"rule"`
	BucketID     string `json:"bucket_id"`
	RetrySeconds int    `json:"retry_seconds"`
	Blocked      bool   `json:"blocked"`
	Exceeded     bool   `json:"exceeded"`
}

// All dimensions are reserved in one transaction. Shared account totals apply
// across keys; row locking works across processes and survives app replacement.
func (s *Store) ConsumeRisk(ctx context.Context, checks []RiskConsumption) ([]RiskDecision, error) {
	sort.Slice(checks, func(i, j int) bool {
		return checks[i].Rule+checks[i].Identity.ID < checks[j].Rule+checks[j].Identity.ID
	})
	out := []RiskDecision{}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	var now time.Time
	if err = tx.QueryRow(ctx, "SELECT now()").Scan(&now); err != nil {
		return out, err
	}
	for _, check := range checks {
		p := check.Policy
		if p.Mode == "off" {
			continue
		}
		if RiskRuleNames[check.Rule] == "" || !oneOf(p.Mode, "observe", "enforce") || p.Limit < 1 || p.WindowSeconds < 1 || p.CooldownSeconds < 1 {
			return out, invalid("限流规则无效")
		}
		identity := check.Identity
		id := Hash(check.Rule + ":" + identity.Type + ":" + identity.ID)
		raw, _ := json.Marshal(p)
		policy := Hash(string(raw))
		window := now.Add(time.Duration(p.WindowSeconds) * time.Second)
		_, err = tx.Exec(ctx, `INSERT INTO risk_buckets(id,rule,subject_type,subject_id,label,user_id,key_id,policy,mode,window_until) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING`, id, check.Rule, identity.Type, identity.ID, identity.Label, identity.UserID, identity.KeyID, policy, p.Mode, window)
		if err != nil {
			return out, err
		}
		var previous string
		var count int64
		var until time.Time
		var blocked *time.Time
		if err = tx.QueryRow(ctx, "SELECT policy,count,window_until,blocked_until FROM risk_buckets WHERE id=$1 FOR UPDATE", id).Scan(&previous, &count, &until, &blocked); err != nil {
			return out, err
		}
		// A policy change or expired restriction starts a fresh window. Repeated
		// denied requests do not extend the restriction indefinitely.
		if previous != policy || (blocked != nil && !now.Before(*blocked)) || (blocked == nil && !now.Before(until)) {
			count = 0
			until = window
			blocked = nil
		}
		if blocked == nil {
			count++
			if count > int64(p.Limit) {
				end := now.Add(time.Duration(p.CooldownSeconds) * time.Second)
				blocked = &end
			}
		}
		decision := RiskDecision{Rule: check.Rule, BucketID: id}
		if blocked != nil {
			decision.Exceeded = true
			decision.Blocked = p.Mode == "enforce"
			decision.RetrySeconds = int((blocked.Sub(now) + time.Second - 1) / time.Second)
		}
		_, err = tx.Exec(ctx, `UPDATE risk_buckets SET count=$2,window_until=$3,blocked_until=$4,policy=$5,mode=$6,updated_at=now(),label=$7,user_id=$8,key_id=$9 WHERE id=$1`, id, count, until, blocked, policy, p.Mode, identity.Label, identity.UserID, identity.KeyID)
		if err != nil {
			return out, err
		}
		out = append(out, decision)
	}
	return out, tx.Commit(ctx)
}

func (s *Store) ClearLoginRisk(ctx context.Context, email string) error {
	id := Hash("login:login:" + email)
	_, err := s.Pool.Exec(ctx, "DELETE FROM risk_buckets WHERE id=$1 AND rule='login'", id)
	return err
}

func (s *Store) ReleaseRiskLimit(ctx context.Context, actor, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = adminLock(ctx, tx, actor); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "DELETE FROM risk_buckets WHERE id=$1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = audit(ctx, tx, actor, "risk.release", id, map[string]bool{"released": true}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RiskKeyIdentity(ctx context.Context, uid, token string) (RiskIdentity, error) {
	v := RiskIdentity{Type: "key", UserID: uid}
	err := s.Pool.QueryRow(ctx, "SELECT id,name FROM api_keys WHERE user_id=$1 AND token_hash=$2 AND revoked_at IS NULL", uid, Hash(token)).Scan(&v.ID, &v.Label)
	v.KeyID = v.ID
	return v, err
}

func (s *Store) AdminRiskRevokeKey(ctx context.Context, actor, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = adminLock(ctx, tx, actor); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "UPDATE api_keys SET revoked_at=COALESCE(revoked_at,now()) WHERE id=$1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = audit(ctx, tx, actor, "risk.key_revoke", id, map[string]bool{"revoked": true}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

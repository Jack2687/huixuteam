package store

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
	"math/big"
	"strings"
	"time"
)

var ErrAuthRate = errors.New("操作过于频繁，请稍后再试")
var ErrEmailUnavailable = errors.New("邮件服务暂不可用，请联系管理员")

func (s *Store) codeHash(id, email, purpose, code string) string {
	h := hmac.New(sha256.New, s.authCodeKey[:])
	h.Write([]byte(id + "\x00" + email + "\x00" + purpose + "\x00" + code))
	return hex.EncodeToString(h.Sum(nil))
}

// Mandatory limits remain in force even when optional traffic monitoring is off.
func (s *Store) AuthRate(ctx context.Context, ip, email, operation string) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	limit := 30
	if operation == "submit" {
		limit = 60
	}
	entries := []struct {
		key          string
		seconds, max int
	}{{"ip:" + operation + ":" + Hash(ip), 3600, limit}, {"email:" + operation + ":" + Hash(strings.ToLower(strings.TrimSpace(email))), 3600, 10}}
	for _, v := range entries {
		var hits int
		e = tx.QueryRow(ctx, `INSERT INTO auth_email_limits(key) VALUES($1) ON CONFLICT(key) DO UPDATE
   SET hits=CASE WHEN auth_email_limits.window_start<now()-make_interval(secs=>$2) THEN 1 ELSE auth_email_limits.hits+1 END,
   window_start=CASE WHEN auth_email_limits.window_start<now()-make_interval(secs=>$2) THEN now() ELSE auth_email_limits.window_start END RETURNING hits`, v.key, v.seconds).Scan(&hits)
		if e != nil {
			return e
		}
		if hits > v.max {
			if e = tx.Commit(ctx); e != nil {
				return e
			}
			return ErrAuthRate
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) RequestEmailCode(ctx context.Context, email, purpose string) error {
	var e error
	email, e = normalizeEmail(email)
	if e != nil {
		return e
	}
	if purpose != "register" && purpose != "reset" {
		return invalid("验证码用途无效")
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	security, e := readSecurityTx(ctx, tx)
	if e != nil {
		return e
	}
	if purpose == "register" {
		if !security.RegistrationEnabled {
			return fmt.Errorf("%w：当前未开放注册", ErrForbidden)
		}
		if !security.Allows(email) {
			return invalid("该邮箱域名不在注册白名单中")
		}
		if !security.EmailVerificationEnabled {
			return invalid("当前注册无需验证码")
		}
	}
	// Serialize resends, including emails that do not have a challenge yet.
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,718374025))", purpose+":"+email); e != nil {
		return e
	}
	var recent bool
	if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM auth_email_challenges WHERE email=$1 AND purpose=$2 AND created_at>now()-interval '60 seconds')", email, purpose).Scan(&recent); e != nil {
		return e
	}
	if recent {
		return ErrAuthRate
	}
	var id, credential string
	var enabled bool
	e = tx.QueryRow(ctx, "SELECT id,password_hash,NOT disabled AND deleted_at IS NULL FROM users WHERE email=$1", email).Scan(&id, &credential, &enabled)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	deliver := (purpose == "register" && id == "") || (purpose == "reset" && id != "" && enabled)
	if _, _, e = s.mailConfigTx(ctx, tx); e != nil {
		return e
	}
	random, e := rand.Int(rand.Reader, big.NewInt(1000000))
	if e != nil {
		return e
	}
	code := fmt.Sprintf("%06d", random.Int64())
	challenge := ID()
	_, e = tx.Exec(ctx, `INSERT INTO auth_email_challenges(email,purpose,id,code_hash,user_id,credential_hash,expires_at)
 VALUES($1,$2,$3,$4,$5,$6,now()+interval '15 minutes') ON CONFLICT(email,purpose) DO UPDATE
 SET id=EXCLUDED.id,code_hash=EXCLUDED.code_hash,user_id=EXCLUDED.user_id,credential_hash=EXCLUDED.credential_hash,
 attempts=0,expires_at=EXCLUDED.expires_at,consumed_at=NULL,created_at=now()`, email, purpose, challenge, s.codeHash(challenge, email, purpose, code), id, Hash(credential))
	if e != nil {
		return e
	}
	// Superseded jobs cannot send an old code after a new one.
	if _, e = tx.Exec(ctx, "UPDATE mail_jobs SET status='expired',payload=NULL,error_code='superseded' WHERE recipient=$1 AND purpose=$2 AND status IN ('queued','retry')", email, purpose); e != nil {
		return e
	}
	if deliver {
		action := "注册账号"
		if purpose == "reset" {
			action = "重置密码"
		}
		e = s.queueMailTx(ctx, tx, email, purpose, challenge, "回序 HUIXU · "+action+"验证码", "你正在"+action+"。\n\n验证码："+code+"\n\n15 分钟内有效，请勿向他人透露。若非本人操作，请忽略此邮件。", time.Now().Add(15*time.Minute))
		if e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}

// Consume inside the same transaction as the account mutation. Failed guesses
// are committed separately so rolling back registration cannot reset attempts.
func (s *Store) verifyCodeTx(ctx context.Context, tx pgx.Tx, email, purpose, code string) (string, string, error) {
	var id, expected, uid, credential string
	var valid bool
	e := tx.QueryRow(ctx, `SELECT id,code_hash,user_id,credential_hash,expires_at>now() AND consumed_at IS NULL AND attempts<5
 FROM auth_email_challenges WHERE email=$1 AND purpose=$2 FOR UPDATE`, email, purpose).Scan(&id, &expected, &uid, &credential, &valid)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return "", "", e
	}
	if e != nil || !valid {
		return "", "", invalid("验证码无效或已过期，请重新获取")
	}
	actual := s.codeHash(id, email, purpose, code)
	if len(code) != 6 || subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
		if _, e = tx.Exec(ctx, "UPDATE auth_email_challenges SET attempts=attempts+1 WHERE email=$1 AND purpose=$2", email, purpose); e != nil {
			return "", "", e
		}
		if e = tx.Commit(ctx); e != nil {
			return "", "", e
		}
		return "", "", invalid("验证码无效或已过期，请重新获取")
	}
	if _, e = tx.Exec(ctx, "UPDATE auth_email_challenges SET consumed_at=now() WHERE email=$1 AND purpose=$2", email, purpose); e != nil {
		return "", "", e
	}
	return uid, credential, nil
}

type RegistrationInput struct {
	ServiceEligibilityAccepted bool   `json:"service_eligibility_accepted"`
	Invite                     string `json:"invite"`
	Email                      string `json:"email"`
	Name                       string `json:"name"`
	Password                   string `json:"password"`
	Code                       string `json:"code"`
}

func (s *Store) Register(ctx context.Context, v RegistrationInput) error {
	if !v.ServiceEligibilityAccepted {
		return invalid("请先确认你有退款办理经验，并以业务用户身份使用本平台；暂不接受个人用户")
	}
	var e error
	v.Email, e = normalizeEmail(v.Email)
	if e != nil {
		return e
	}
	v.Name = strings.TrimSpace(v.Name)
	if len(v.Name) < 1 || len(v.Name) > 80 {
		return invalid("姓名须为1～80字节")
	}
	if len(v.Password) < 12 || len(v.Password) > 72 {
		return invalid("密码须为12～72字节")
	}
	hashed, e := bcrypt.GenerateFromPassword([]byte(v.Password), bcrypt.DefaultCost)
	if e != nil {
		return e
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	cfg, e := readSecurityTx(ctx, tx)
	if e != nil {
		return e
	}
	if !cfg.RegistrationEnabled {
		return fmt.Errorf("%w：当前未开放注册", ErrForbidden)
	}
	if !cfg.Allows(v.Email) {
		return invalid("该邮箱域名不在注册白名单中")
	}
	if cfg.EmailVerificationEnabled {
		if _, _, e = s.verifyCodeTx(ctx, tx, v.Email, "register", v.Code); e != nil {
			return e
		}
	}
	// Read pricing inside registration's transaction; never accept a client price.
	var fee int64
	if e = tx.QueryRow(ctx, "SELECT default_service_fee FROM site_service_settings WHERE id FOR SHARE").Scan(&fee); e != nil {
		return e
	}
	var inviter *string
	if v.Invite != "" {
		if len(v.Invite) > 80 {
			return invalid("邀请标识无效")
		}
		var owner string
		if e = tx.QueryRow(ctx, "SELECT id FROM users WHERE invite_code=$1 AND NOT disabled AND deleted_at IS NULL FOR SHARE", v.Invite).Scan(&owner); errors.Is(e, pgx.ErrNoRows) {
			return invalid("邀请标识无效")
		} else if e != nil {
			return e
		}
		inviter = &owner
	}
	tag, e := tx.Exec(ctx, "INSERT INTO users(id,email,name,password_hash,role,service_fee,invited_by) VALUES($1,$2,$3,$4,'member',$5,$6) ON CONFLICT(email) DO NOTHING", ID(), v.Email, v.Name, string(hashed), fee, inviter)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return invalid("无法注册该邮箱，请登录或使用忘记密码")
	}
	return tx.Commit(ctx)
}
func (s *Store) ResetPasswordByEmail(ctx context.Context, email, code, password string) error {
	var e error
	email, e = normalizeEmail(email)
	if e != nil {
		return e
	}
	if len(password) < 12 || len(password) > 72 {
		return invalid("密码须为12～72字节")
	}
	hashed, e := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if e != nil {
		return e
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	// Same order as admin account edits. A later password change invalidates codes.
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(718374022)"); e != nil {
		return e
	}
	var uid, oldHash string
	e = tx.QueryRow(ctx, "SELECT id,password_hash FROM users WHERE email=$1 AND NOT disabled AND deleted_at IS NULL FOR UPDATE", email).Scan(&uid, &oldHash)
	if errors.Is(e, pgx.ErrNoRows) {
		return invalid("验证码无效或已过期，请重新获取")
	}
	if e != nil {
		return e
	}
	codeUID, credential, e := s.verifyCodeTx(ctx, tx, email, "reset", code)
	if e != nil {
		return e
	}
	if codeUID != uid || credential != Hash(oldHash) {
		return invalid("验证码无效或已过期，请重新获取")
	}
	if _, e = tx.Exec(ctx, "UPDATE users SET password_hash=$1 WHERE id=$2", string(hashed), uid); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "DELETE FROM sessions WHERE user_id=$1", uid); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "UPDATE api_keys SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL", uid); e != nil {
		return e
	}
	if e = audit(ctx, tx, uid, "user.password_reset_email", uid, map[string]bool{"sessions_revoked": true, "keys_revoked": true}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

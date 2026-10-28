package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"llm-refund/internal/refundprovider"
)

//go:embed schema.sql
var schema string

var ErrNotFound = errors.New("记录不存在")
var ErrBalance = errors.New("可用积分不足，请联系服务专员充值")
var ErrConflict = errors.New("幂等键已用于不同的申请内容")
var ErrDuplicate = errors.New("账号已有正在处理的申请，请勿重复提交")
var ErrState = errors.New("当前申请状态不支持此操作")
var ErrSubmissionPaused = errors.New("当前账户暂不能提交新申请")

var ErrForbidden = errors.New("没有执行此操作的权限")
var ErrInvalid = errors.New("参数错误")

type Store struct {
	Pool         *pgxpool.Pool
	cipher       cipher.AEAD
	runtimeReady atomic.Bool
}
type User struct {
	ID          string   `json:"id"`
	Email       string   `json:"email"`
	Name        string   `json:"name"`
	Balance     int64    `json:"balance"`
	Frozen      int64    `json:"frozen"`
	ServiceFee  int64    `json:"service_fee"`
	Role        string   `json:"role"`
	Disabled    bool     `json:"disabled"`
	Permissions []string `json:"permissions"`
}
type Refund struct {
	ErrorCode  string    `json:"error_code,omitempty"`
	HTTPStatus int       `json:"http_status,omitempty"`
	ID         string    `json:"id"`
	Email      string    `json:"email"`
	Status     string    `json:"status"`
	Fee        int64     `json:"fee"`
	FeeStatus  string    `json:"fee_status"`
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}
type Event struct {
	ID        int64     `json:"id"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}
type Key struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}
type Entry struct {
	ID           int64     `json:"id"`
	Kind         string    `json:"kind"`
	Amount       int64     `json:"amount"`
	BalanceAfter int64     `json:"balance_after"`
	RefundID     string    `json:"refund_id"`
	Note         string    `json:"note"`
	CreatedAt    time.Time `json:"created_at"`
}
type Notification struct {
	ID        int64      `json:"id"`
	Message   string     `json:"message"`
	ReadAt    *time.Time `json:"read_at"`
	CreatedAt time.Time  `json:"created_at"`
}

func ID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func Token() string        { return ID() + ID() }
func Hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func Open(ctx context.Context, dsn string, master []byte) (*Store, error) {
	if len(master) != 32 {
		return nil, errors.New("MASTER_KEY must be 32 bytes")
	}
	block, err := aes.NewCipher(master)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid DATABASE_URL")
	}
	cfg.MaxConns = 10
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		pool.Close()
		return nil, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(718374021)")
	if err == nil {
		_, err = tx.Exec(ctx, schema)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{Pool: pool, cipher: aead}, nil
}
func (s *Store) Encrypt(id string, a refundprovider.Account) ([]byte, error) {
	raw, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, s.cipher.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.cipher.Seal(nonce, nonce, raw, []byte(id)), nil
}

// Credentials are for the server-side worker only. Never return them to the UI.
func (s *Store) Credentials(ctx context.Context, uid, id string) (refundprovider.Account, error) {
	var data []byte
	var a refundprovider.Account
	if err := s.Pool.QueryRow(ctx, "SELECT credentials FROM refunds WHERE user_id=$1 AND id=$2", uid, id).Scan(&data); err != nil {
		return a, ErrNotFound
	}
	n := s.cipher.NonceSize()
	if len(data) < n {
		return a, errors.New("invalid encrypted credentials")
	}
	raw, err := s.cipher.Open(nil, data[:n], data[n:], []byte(id))
	if err != nil {
		return a, err
	}
	err = json.Unmarshal(raw, &a)
	return a, err
}
func ValidateAccounts(accounts []refundprovider.Account) error {
	if len(accounts) < 1 || len(accounts) > 500 {
		return errors.New("每批须包含 1～500 个账号")
	}
	seen := map[string]bool{}
	for i := range accounts {
		a := &accounts[i]
		a.Email = strings.ToLower(strings.TrimSpace(a.Email))
		a.TOTPSecret = strings.TrimSpace(a.TOTPSecret)
		addr, err := mail.ParseAddress(a.Email)
		if err != nil || addr.Address != a.Email || len(a.Email) > 254 {
			return fmt.Errorf("第 %d 行邮箱格式不正确", i+1)
		}
		if len(a.Password) == 0 || len(a.Password) > 1024 {
			return fmt.Errorf("第 %d 行密码不能为空且不能超过 1024 字节", i+1)
		}
		secret := a.TOTPSecret
		if strings.HasPrefix(secret, "otpauth://") {
			u, e := url.Parse(secret)
			if e != nil || u.Host != "totp" {
				return fmt.Errorf("第 %d 行 2FA 链接不正确", i+1)
			}
			secret = u.Query().Get("secret")
		}
		secret = strings.ToUpper(strings.ReplaceAll(secret, " ", ""))
		secret = strings.TrimRight(secret, "=")
		decoded, e := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
		if e != nil || len(decoded) < 10 || len(secret) > 256 {
			return fmt.Errorf("第 %d 行请填写有效的 Base32 2FA 密钥或 otpauth 链接，不是六位验证码", i+1)
		}
		a.TOTPSecret = secret
		if seen[a.Email] {
			return fmt.Errorf("第 %d 行账号重复", i+1)
		}
		seen[a.Email] = true
	}
	return nil
}
func (s *Store) Bootstrap(ctx context.Context, email, password, name string) error {
	if email == "" || password == "" {
		return nil
	}
	if len(password) < 12 || len(password) > 72 {
		return errors.New("bootstrap password must be 12–72 bytes")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return errors.New("invalid bootstrap email")
	}
	if name == "" {
		name = "Admin"
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
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(718374022)"); err != nil {
		return err
	}
	// Bootstrap only once. Editing an administrator's email must not recreate
	// the old login from environment variables on the next application restart.
	_, err = tx.Exec(ctx, "INSERT INTO users(id,email,name,password_hash,role) SELECT $1,$2,$3,$4,'admin' WHERE NOT EXISTS(SELECT 1 FROM users WHERE role='admin') ON CONFLICT(email) DO NOTHING", ID(), strings.ToLower(strings.TrimSpace(email)), name, string(hash))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const userColumns = "id,email,name,balance,frozen,service_fee,role,disabled"

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Balance, &u.Frozen, &u.ServiceFee, &u.Role, &u.Disabled)
	u.Permissions = Permissions(u.Role)
	return u, err
}
func (s *Store) User(ctx context.Context, id string) (User, error) {
	return scanUser(s.Pool.QueryRow(ctx, "SELECT "+userColumns+" FROM users WHERE id=$1 AND NOT disabled AND deleted_at IS NULL", id))
}
func (s *Store) Login(ctx context.Context, email, password string) (User, error) {
	var hash, id string
	err := s.Pool.QueryRow(ctx, "SELECT id,password_hash FROM users WHERE email=$1 AND NOT disabled AND deleted_at IS NULL", strings.ToLower(strings.TrimSpace(email))).Scan(&id, &hash)
	// Always perform a bcrypt comparison, even for unknown users.
	if err != nil {
		hash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
	}
	check := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err != nil || check != nil {
		return User{}, errors.New("邮箱或密码不正确")
	}
	return s.User(ctx, id)
}
func (s *Store) NewSession(ctx context.Context, uid string) (string, error) {
	token := Token()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var activeID string
	if err = tx.QueryRow(ctx, "SELECT id FROM users WHERE id=$1 AND NOT disabled AND deleted_at IS NULL FOR UPDATE", uid).Scan(&activeID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrForbidden
		}
		return "", err
	}
	_, err = tx.Exec(ctx, "DELETE FROM sessions WHERE expires_at < now()")
	if err == nil {
		_, err = tx.Exec(ctx, "INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)", Hash(token), uid, time.Now().Add(7*24*time.Hour))
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return token, err
}
func (s *Store) Authenticate(ctx context.Context, token string, key bool) (User, error) {
	var id string
	var err error
	if key {
		err = s.Pool.QueryRow(ctx, "UPDATE api_keys SET last_used_at=now() WHERE token_hash=$1 AND revoked_at IS NULL RETURNING user_id", Hash(token)).Scan(&id)
	} else {
		err = s.Pool.QueryRow(ctx, "SELECT user_id FROM sessions WHERE token_hash=$1 AND expires_at>now()", Hash(token)).Scan(&id)
	}
	if err != nil {
		return User{}, err
	}
	return s.User(ctx, id)
}
func (s *Store) Logout(ctx context.Context, token string) error {
	_, err := s.Pool.Exec(ctx, "DELETE FROM sessions WHERE token_hash=$1", Hash(token))
	return err
}

const refundColumns = "id,email,status,fee,fee_status,message,created_at,updated_at"

func scanRefund(row pgx.Row) (Refund, error) {
	var r Refund
	err := row.Scan(&r.ID, &r.Email, &r.Status, &r.Fee, &r.FeeStatus, &r.Message, &r.CreatedAt, &r.UpdatedAt)
	sanitizeRefund(&r)
	return r, err
}
func (s *Store) Refund(ctx context.Context, uid, id string) (Refund, error) {
	r, err := scanRefund(s.Pool.QueryRow(ctx, "SELECT "+refundColumns+" FROM refunds WHERE user_id=$1 AND id=$2", uid, id))
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return r, err
}
func (s *Store) Events(ctx context.Context, uid, id string) ([]Event, error) {
	rows, err := s.Pool.Query(ctx, "SELECT e.id,e.message,e.created_at FROM refund_events e JOIN refunds r ON r.id=e.refund_id WHERE r.user_id=$1 AND r.id=$2 AND NOT e.internal ORDER BY e.id", uid, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err = rows.Scan(&e.ID, &e.Message, &e.CreatedAt); err != nil {
			return nil, err
		}
		if !publicProgress(e.Message) {
			continue
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) ListRefunds(ctx context.Context, uid, q, status string, page, size int) ([]Refund, int, error) {
	where := " FROM refunds WHERE user_id=$1 AND ($2='' OR position(lower($2) in lower(email))>0 OR position(lower($2) in id)>0) AND ($3='' OR status=$3 OR ($3='active' AND status IN ('queued','processing','verifying')))"
	var count int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*)"+where, uid, q, status).Scan(&count); err != nil {
		return nil, 0, err
	}
	rows, err := s.Pool.Query(ctx, "SELECT "+refundColumns+where+" ORDER BY created_at DESC,id LIMIT $4 OFFSET $5", uid, q, status, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Refund{}
	for rows.Next() {
		r, e := scanRefund(rows)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, r)
	}
	return out, count, rows.Err()
}
func (s *Store) CreateRefunds(ctx context.Context, uid, key string, accounts []refundprovider.Account) ([]string, bool, error) {
	if err := ValidateAccounts(accounts); err != nil {
		return nil, false, err
	}
	if len(key) < 8 || len(key) > 128 {
		return nil, false, errors.New("Idempotency-Key 长度必须为 8～128 个字符")
	}
	raw, _ := json.Marshal(accounts)
	requestHash := Hash(string(raw))
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(718374024)"); err != nil {
		return nil, false, err
	}
	var balance, frozen, fee int64
	if err = tx.QueryRow(ctx, "SELECT balance,frozen,service_fee FROM users WHERE id=$1 AND NOT disabled AND deleted_at IS NULL FOR UPDATE", uid).Scan(&balance, &frozen, &fee); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, ErrForbidden
		}
		return nil, false, err
	}
	var oldHash string
	var ids []string
	err = tx.QueryRow(ctx, "SELECT request_hash,refund_ids FROM idempotency WHERE user_id=$1 AND key=$2", uid, key).Scan(&oldHash, &ids)
	if err == nil {
		if oldHash != requestHash {
			return nil, false, ErrConflict
		}
		return ids, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}
	var paused bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM risk_user_controls WHERE user_id=$1 AND paused AND (paused_until IS NULL OR paused_until>now()))", uid).Scan(&paused); err != nil {
		return nil, false, err
	}
	if paused {
		return nil, false, ErrSubmissionPaused
	}
	total := int64(len(accounts)) * fee
	if balance-frozen < total {
		return nil, false, ErrBalance
	}
	ids = make([]string, 0, len(accounts))
	for _, a := range accounts {
		var exists bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM refunds WHERE lower(email)=$1 AND status IN ('queued','processing','verifying','attention','succeeded'))", a.Email).Scan(&exists); err != nil {
			return nil, false, err
		}
		if exists {
			return nil, false, ErrDuplicate
		}
		id := ID()
		encrypted, e := s.Encrypt(id, a)
		if e != nil {
			return nil, false, e
		}
		if _, err = tx.Exec(ctx, "INSERT INTO refunds(id,user_id,email,credentials,fee) VALUES($1,$2,$3,$4,$5)", id, uid, a.Email, encrypted, fee); err != nil {
			return nil, false, err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO refund_events(refund_id,message) VALUES($1,'申请已受理')", id); err != nil {
			return nil, false, err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO ledger(user_id,refund_id,kind,amount,balance_after) VALUES($1,$2,'freeze',$3,$4)", uid, id, fee, balance); err != nil {
			return nil, false, err
		}
		ids = append(ids, id)
	}
	if _, err = tx.Exec(ctx, "UPDATE users SET frozen=frozen+$2 WHERE id=$1", uid, total); err != nil {
		return nil, false, err
	}
	encoded, _ := json.Marshal(ids)
	if _, err = tx.Exec(ctx, "INSERT INTO idempotency(user_id,key,request_hash,refund_ids) VALUES($1,$2,$3,$4)", uid, key, requestHash, encoded); err != nil {
		return nil, false, err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO notifications(user_id,message) VALUES($1,$2)", uid, fmt.Sprintf("已受理 %d 笔退款申请，等待处理。", len(ids))); err != nil {
		return nil, false, err
	}
	return ids, false, tx.Commit(ctx)
}

// Transition atomically records progress and settles/releases the fee.
// Repeated terminal callbacks do not charge twice; cancelled requests cannot succeed.
func (s *Store) Transition(ctx context.Context, uid, id, to, message string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = transitionTx(ctx, tx, uid, id, to, message); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func transitionTx(ctx context.Context, tx pgx.Tx, uid, id, to, message string) error {
	var balance int64
	if err := tx.QueryRow(ctx, "SELECT balance FROM users WHERE id=$1 FOR UPDATE", uid).Scan(&balance); err != nil {
		return err
	}
	r, err := scanRefund(tx.QueryRow(ctx, "SELECT "+refundColumns+" FROM refunds WHERE user_id=$1 AND id=$2 FOR UPDATE", uid, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if r.Status == to {
		return nil
	}
	allowed := map[string]map[string]bool{
		"queued": {"cancelled": true, "processing": true}, "processing": {"verifying": true, "succeeded": true, "failed": true, "banned": true, "attention": true},
		"verifying": {"processing": true, "succeeded": true, "failed": true, "attention": true, "banned": true}, "attention": {"verifying": true, "processing": true, "succeeded": true, "failed": true, "banned": true},
	}
	if !allowed[r.Status][to] {
		return ErrState
	}
	feeStatus := r.FeeStatus
	kind := ""
	delta := r.Fee
	if to == "succeeded" {
		if feeStatus != "frozen" {
			return ErrState
		}
		kind = "settlement"
		feeStatus = "settled"
		balance -= r.Fee
		delta = -r.Fee
		_, err = tx.Exec(ctx, "UPDATE users SET balance=balance-$2,frozen=frozen-$2 WHERE id=$1", uid, r.Fee)
	} else if to == "cancelled" || to == "failed" || to == "banned" {
		if feeStatus != "frozen" {
			return ErrState
		}
		kind = "release"
		feeStatus = "released"
		_, err = tx.Exec(ctx, "UPDATE users SET frozen=frozen-$2 WHERE id=$1", uid, r.Fee)
	}
	if err != nil {
		return err
	}
	if kind != "" {
		if _, err = tx.Exec(ctx, "INSERT INTO ledger(user_id,refund_id,kind,amount,balance_after) VALUES($1,$2,$3,$4,$5)", uid, id, kind, delta, balance); err != nil {
			return err
		}
	}
	message = publicRefundMessage(to, message)
	if _, err = tx.Exec(ctx, "UPDATE refunds SET status=$3,fee_status=$4,message=$5,updated_at=now() WHERE user_id=$1 AND id=$2", uid, id, to, feeStatus, message); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO refund_events(refund_id,message) VALUES($1,$2)", id, message); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO notifications(user_id,message) VALUES($1,$2)", uid, r.Email+"："+message); err != nil {
		return err
	}
	return nil
}
func (s *Store) Credit(ctx context.Context, email string, amount int64, note string) error {
	if amount <= 0 || amount > 100000000 {
		return errors.New("充值积分须大于 0 且不超过 1,000,000")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var id string
	var balance int64
	if err = tx.QueryRow(ctx, "UPDATE users SET balance=balance+$2 WHERE email=$1 AND deleted_at IS NULL RETURNING id,balance", strings.ToLower(strings.TrimSpace(email)), amount).Scan(&id, &balance); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	_, err = tx.Exec(ctx, "INSERT INTO ledger(user_id,kind,amount,balance_after,note) VALUES($1,'credit',$2,$3,$4)", id, amount, balance, note)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) Billing(ctx context.Context, uid string, page, size int) ([]Entry, int, error) {
	var total int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM ledger WHERE user_id=$1", uid).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.Pool.Query(ctx, "SELECT id,kind,amount,balance_after,COALESCE(refund_id,''),note,created_at FROM ledger WHERE user_id=$1 ORDER BY id DESC LIMIT $2 OFFSET $3", uid, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		if err = rows.Scan(&e.ID, &e.Kind, &e.Amount, &e.BalanceAfter, &e.RefundID, &e.Note, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}
func (s *Store) Keys(ctx context.Context, uid string) ([]Key, error) {
	rows, err := s.Pool.Query(ctx, "SELECT id,name,prefix,created_at,last_used_at,revoked_at FROM api_keys WHERE user_id=$1 ORDER BY created_at DESC", uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		var k Key
		if err = rows.Scan(&k.ID, &k.Name, &k.Prefix, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
func (s *Store) CreateKey(ctx context.Context, uid, name string) (string, error) {
	name = strings.TrimSpace(name)
	if len(name) < 1 || len(name) > 80 {
		return "", errors.New("密钥名称长度须为 1～80 字节")
	}
	token := "hx_" + Token()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var activeID string
	if err = tx.QueryRow(ctx, "SELECT id FROM users WHERE id=$1 AND NOT disabled AND deleted_at IS NULL FOR UPDATE", uid).Scan(&activeID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrForbidden
		}
		return "", err
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM api_keys WHERE user_id=$1 AND revoked_at IS NULL", uid).Scan(&count); err != nil {
		return "", err
	}
	if count >= 20 {
		return "", errors.New("最多保留 20 个有效密钥")
	}
	_, err = tx.Exec(ctx, "INSERT INTO api_keys(id,user_id,name,token_hash,prefix) VALUES($1,$2,$3,$4,$5)", ID(), uid, name, Hash(token), token[:12])
	if err != nil {
		return "", err
	}
	return token, tx.Commit(ctx)
}
func (s *Store) RevokeKey(ctx context.Context, uid, id string) error {
	r, err := s.Pool.Exec(ctx, "UPDATE api_keys SET revoked_at=COALESCE(revoked_at,now()) WHERE user_id=$1 AND id=$2", uid, id)
	if err == nil && r.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
func (s *Store) Notifications(ctx context.Context, uid string) ([]Notification, error) {
	rows, err := s.Pool.Query(ctx, "SELECT id,message,read_at,created_at FROM notifications WHERE user_id=$1 ORDER BY id DESC LIMIT 30", uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var n Notification
		if err = rows.Scan(&n.ID, &n.Message, &n.ReadAt, &n.CreatedAt); err != nil {
			return nil, err
		}
		n.Message = publicNotification(n.Message)
		out = append(out, n)
	}
	return out, rows.Err()
}
func (s *Store) ReadNotifications(ctx context.Context, uid string) error {
	_, err := s.Pool.Exec(ctx, "UPDATE notifications SET read_at=now() WHERE user_id=$1 AND read_at IS NULL", uid)
	return err
}
func (s *Store) Dashboard(ctx context.Context, uid string) (map[string]any, error) {
	counts := map[string]int{"total": 0, "succeeded": 0, "active": 0, "attention": 0}
	rows, err := s.Pool.Query(ctx, "SELECT status,count(*) FROM refunds WHERE user_id=$1 GROUP BY status", uid)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var status string
		var n int
		if err = rows.Scan(&status, &n); err != nil {
			rows.Close()
			return nil, err
		}
		counts[status] = n
		counts["total"] += n
		if status == "queued" || status == "processing" || status == "verifying" {
			counts["active"] += n
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	trend := []map[string]any{}
	rows, err = s.Pool.Query(ctx, `SELECT d::date::text,count(r.id) FROM generate_series((now() AT TIME ZONE 'Asia/Shanghai')::date-13,(now() AT TIME ZONE 'Asia/Shanghai')::date,'1 day') d LEFT JOIN refunds r ON r.user_id=$1 AND (r.created_at AT TIME ZONE 'Asia/Shanghai')::date=d::date GROUP BY d ORDER BY d`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		var n int
		if err = rows.Scan(&d, &n); err != nil {
			return nil, err
		}
		trend = append(trend, map[string]any{"date": d, "count": n})
	}
	return map[string]any{"counts": counts, "trend": trend, "provider_ready": s.ProviderReady(ctx)}, rows.Err()
}

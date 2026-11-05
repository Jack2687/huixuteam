package store

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

type mailContent struct {
	Subject string
	Body    string
}
type MailLog struct {
	ID        string     `json:"id"`
	Recipient string     `json:"recipient"`
	Purpose   string     `json:"purpose"`
	Status    string     `json:"status"`
	Attempts  int        `json:"attempts"`
	ErrorCode string     `json:"error_code"`
	CreatedAt time.Time  `json:"created_at"`
	SentAt    *time.Time `json:"sent_at"`
}

func (s *Store) queueMailTx(ctx context.Context, tx pgx.Tx, email, purpose, challenge, subject, body string, expires time.Time) error {
	var count int
	if e := tx.QueryRow(ctx, "SELECT count(*) FROM mail_jobs WHERE status IN ('queued','retry','sending')").Scan(&count); e != nil {
		return e
	}
	if count >= 500 {
		return ErrEmailUnavailable
	}
	id := ID()
	encrypted, e := s.sealSecret("mail-job:"+id, mailContent{subject, body})
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, "INSERT INTO mail_jobs(id,recipient,purpose,challenge_id,payload,expires_at) VALUES($1,$2,$3,$4,$5,$6)", id, email, purpose, challenge, encrypted, expires)
	return e
}
func (s *Store) QueueTestMail(ctx context.Context, actor, email string) error {
	var e error
	email, e = normalizeEmail(email)
	if e != nil {
		return e
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = adminLock(ctx, tx, actor); e != nil {
		return e
	}
	_, _, e = s.mailConfigTx(ctx, tx)
	if e != nil {
		return e
	}
	var recent bool
	if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM mail_jobs WHERE purpose='test' AND created_at>now()-interval '30 seconds')").Scan(&recent); e != nil {
		return e
	}
	if recent {
		return ErrAuthRate
	}
	if e = s.queueMailTx(ctx, tx, email, "test", "", "回序 HUIXU · 邮件测试", "这是一封来自回序 HUIXU 的测试邮件。收到此邮件说明邮件发送配置可用。", time.Now().Add(15*time.Minute)); e != nil {
		return e
	}
	if e = audit(ctx, tx, actor, "site.mail.test", "site", map[string]string{"recipient": email}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) MailLogs(ctx context.Context, page, size int) ([]MailLog, int, error) {
	var count int
	if e := s.Pool.QueryRow(ctx, "SELECT count(*) FROM mail_jobs").Scan(&count); e != nil {
		return nil, 0, e
	}
	rows, e := s.Pool.Query(ctx, "SELECT id,recipient,purpose,status,attempts,error_code,created_at,sent_at FROM mail_jobs ORDER BY created_at DESC,id LIMIT $1 OFFSET $2", size, (page-1)*size)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []MailLog{}
	for rows.Next() {
		var v MailLog
		if e = rows.Scan(&v.ID, &v.Recipient, &v.Purpose, &v.Status, &v.Attempts, &v.ErrorCode, &v.CreatedAt, &v.SentAt); e != nil {
			return nil, 0, e
		}
		out = append(out, v)
	}
	return out, count, rows.Err()
}
func (s *Store) mailConfigTx(ctx context.Context, tx pgx.Tx) (SiteMail, string, error) {
	var v SiteMail
	var raw, secret []byte
	var password string
	e := tx.QueryRow(ctx, "SELECT mail,mail_password FROM site_settings WHERE id FOR SHARE").Scan(&raw, &secret)
	if e != nil {
		return v, "", e
	}
	if e = json.Unmarshal(raw, &v); e != nil {
		return v, "", e
	}
	v.PasswordConfigured = len(secret) > 0
	if !v.Ready() {
		return v, "", ErrEmailUnavailable
	}
	if len(secret) > 0 {
		e = s.openSecret("site-mail", secret, &password)
	}
	return v, password, e
}

// LoginAuth supports providers that advertise LOGIN but not PLAIN. The caller
// authenticates only after certificate-verified TLS, with no insecure fallback.
type smtpLogin struct {
	username, password string
	step               int
}

func (a *smtpLogin) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", []byte(a.username), nil
}
func (a *smtpLogin) Next(_ []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	a.step++
	if a.step == 1 {
		return []byte(a.password), nil
	}
	return nil, errors.New("smtp authentication rejected")
}
func sendSMTP(ctx context.Context, cfg SiteMail, password, to string, content mailContent) error {
	address := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	dialer := net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var e error
	tc := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}
	if cfg.Encryption == "tls" {
		conn, e = (&tls.Dialer{NetDialer: &dialer, Config: tc}).DialContext(ctx, "tcp", address)
	} else {
		conn, e = dialer.DialContext(ctx, "tcp", address)
	}
	if e != nil {
		return e
	}
	defer conn.Close()
	deadline := time.Now().Add(20 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	client, e := smtp.NewClient(conn, cfg.Host)
	if e != nil {
		return e
	}
	defer client.Close()
	if cfg.Encryption == "starttls" {
		if e = client.StartTLS(tc); e != nil {
			return e
		}
	}
	if cfg.Username != "" {
		if _, ok := client.TLSConnectionState(); !ok {
			return errors.New("smtp TLS required")
		}
		_, methods := client.Extension("AUTH")
		var auth smtp.Auth = smtp.PlainAuth("", cfg.Username, password, cfg.Host)
		if !strings.Contains(strings.ToUpper(methods), "PLAIN") && strings.Contains(strings.ToUpper(methods), "LOGIN") {
			auth = &smtpLogin{username: cfg.Username, password: password}
		}
		if e = client.Auth(auth); e != nil {
			return e
		}
	}
	if e = client.Mail(cfg.FromEmail); e != nil {
		return e
	}
	if e = client.Rcpt(to); e != nil {
		return e
	}
	writer, e := client.Data()
	if e != nil {
		return e
	}
	from := (&mail.Address{Name: cfg.FromName, Address: cfg.FromEmail}).String()
	body := base64.StdEncoding.EncodeToString([]byte(content.Body))
	var wrapped strings.Builder
	for len(body) > 76 {
		wrapped.WriteString(body[:76] + "\r\n")
		body = body[76:]
	}
	wrapped.WriteString(body + "\r\n")
	message := "From: " + from + "\r\nTo: " + to + "\r\nSubject: " + mime.QEncoding.Encode("UTF-8", content.Subject) + "\r\nDate: " + time.Now().Format(time.RFC1123Z) + "\r\nMessage-ID: <" + ID() + "@" + strings.Split(cfg.FromEmail, "@")[1] + ">\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n" + wrapped.String()
	if _, e = writer.Write([]byte(message)); e != nil {
		return e
	}
	if e = writer.Close(); e != nil {
		return e
	}
	// DATA acknowledgement confirms acceptance; QUIT failure must not resend.
	_ = client.Quit()
	return nil
}

// DeliverOneMail uses a durable lease. No credentials or code enter mail logs.
func (s *Store) DeliverOneMail(ctx context.Context) (bool, error) {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return false, e
	}
	defer tx.Rollback(ctx)
	// Use the same lock order as enqueue/settings changes: settings, then jobs.
	// Otherwise a resend plus a waiting settings writer can deadlock delivery.
	security, e := readSecurityTx(ctx, tx)
	if e != nil {
		return false, e
	}
	var id, recipient, challenge, purpose string
	var payload []byte
	var attempts int
	lease := ID()
	e = tx.QueryRow(ctx, `SELECT id,recipient,purpose,challenge_id,payload,attempts FROM mail_jobs
 WHERE status IN ('queued','retry','sending') AND next_attempt_at<=now() ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id, &recipient, &purpose, &challenge, &payload, &attempts)
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	var valid bool
	e = tx.QueryRow(ctx, `SELECT expires_at>now() AND attempts<3 AND ($2='' OR EXISTS(SELECT 1 FROM auth_email_challenges c WHERE c.id=$2 AND c.consumed_at IS NULL AND c.expires_at>now() AND c.attempts<5)) FROM mail_jobs WHERE id=$1`, id, challenge).Scan(&valid)
	if e != nil {
		return false, e
	}
	if purpose == "register" {
		valid = valid && security.RegistrationEnabled && security.EmailVerificationEnabled && security.Allows(recipient)
	}
	if !valid {
		_, e = tx.Exec(ctx, "UPDATE mail_jobs SET status='expired',payload=NULL,error_code='expired_or_replaced',lease_token='' WHERE id=$1", id)
		if e == nil {
			e = tx.Commit(ctx)
		}
		return true, e
	}
	cfg, password, e := s.mailConfigTx(ctx, tx)
	if e != nil {
		return false, e
	}
	if _, e = tx.Exec(ctx, "UPDATE mail_jobs SET status='sending',attempts=attempts+1,lease_token=$2,next_attempt_at=now()+interval '60 seconds' WHERE id=$1", id, lease); e != nil {
		return false, e
	}
	if e = tx.Commit(ctx); e != nil {
		return false, e
	}
	var content mailContent
	e = s.openSecret("mail-job:"+id, payload, &content)
	if e == nil {
		e = sendSMTP(ctx, cfg, password, recipient, content)
	}
	status, reason := "sent", ""
	if e != nil {
		status = "retry"
		reason = "smtp_delivery_failed"
		if attempts+1 >= 3 {
			status = "failed"
		}
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := s.Pool.Exec(finishCtx, `UPDATE mail_jobs SET status=$3,error_code=$4,lease_token='',next_attempt_at=now()+interval '30 seconds',
 sent_at=CASE WHEN $3='sent' THEN now() ELSE sent_at END,payload=CASE WHEN $3 IN ('sent','failed') THEN NULL ELSE payload END WHERE id=$1 AND lease_token=$2`, id, lease, status, reason)
	return true, err
}
func (s *Store) RunMailQueue(ctx context.Context) {
	nextCleanup := time.Time{}
	for ctx.Err() == nil {
		if time.Now().After(nextCleanup) {
			_, e := s.Pool.Exec(ctx, `DELETE FROM auth_email_limits WHERE window_start<now()-interval '1 day';
    DELETE FROM auth_email_challenges WHERE expires_at<now()-interval '1 day';
    DELETE FROM mail_jobs WHERE created_at<now()-interval '30 days' AND status NOT IN ('queued','sending','retry')`)
			if e != nil && ctx.Err() == nil {
				slog.Error("mail retention unavailable")
			}
			nextCleanup = time.Now().Add(time.Hour)
		}
		task, cancel := context.WithTimeout(ctx, 25*time.Second)
		worked, e := s.DeliverOneMail(task)
		cancel()
		if e != nil && ctx.Err() == nil {
			slog.Error("mail queue unavailable")
		}
		delay := 2 * time.Second
		if worked && e == nil {
			delay = 100 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

func (s *Store) MailConnectionTest(ctx context.Context, actor string) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = adminLock(ctx, tx, actor); e != nil {
		return e
	}
	cfg, password, e := s.mailConfigTx(ctx, tx)
	if e != nil {
		return e
	}
	if e = audit(ctx, tx, actor, "site.mail.connection_test", "site", map[string]string{"host": cfg.Host}); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	// Test SMTP greeting/TLS/auth only; no email is sent.
	return probeSMTP(ctx, cfg, password)
}
func probeSMTP(ctx context.Context, cfg SiteMail, password string) error {
	d := &net.Dialer{Timeout: 8 * time.Second}
	address := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	tc := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	var e error
	if cfg.Encryption == "tls" {
		conn, e = (&tls.Dialer{NetDialer: d, Config: tc}).DialContext(ctx, "tcp", address)
	} else {
		conn, e = d.DialContext(ctx, "tcp", address)
	}
	if e != nil {
		return fmt.Errorf("%w：连接失败，请检查主机、端口和加密方式", ErrEmailUnavailable)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(12 * time.Second))
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	c, e := smtp.NewClient(conn, cfg.Host)
	if e != nil {
		return ErrEmailUnavailable
	}
	defer c.Close()
	if cfg.Encryption == "starttls" {
		if e = c.StartTLS(tc); e != nil {
			return fmt.Errorf("%w：TLS握手失败", ErrEmailUnavailable)
		}
	}
	if cfg.Username != "" {
		if _, ok := c.TLSConnectionState(); !ok {
			return ErrEmailUnavailable
		}
		_, methods := c.Extension("AUTH")
		var a smtp.Auth = smtp.PlainAuth("", cfg.Username, password, cfg.Host)
		if !strings.Contains(strings.ToUpper(methods), "PLAIN") && strings.Contains(strings.ToUpper(methods), "LOGIN") {
			a = &smtpLogin{username: cfg.Username, password: password}
		}
		if e = c.Auth(a); e != nil {
			return fmt.Errorf("%w：SMTP认证失败", ErrEmailUnavailable)
		}
	}
	return nil
}

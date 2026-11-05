package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
	"strings"
	"sync"
	"testing"
)

func setupSite(t *testing.T) (*store.Store, store.User, *testutil.MailSink) {
	t.Helper()
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	sink := testutil.SMTP(t)
	e := s.SaveSiteMail(context.Background(), a.ID, store.MailInput{SiteMail: store.SiteMail{Host: sink.Host, Port: sink.Port, Encryption: "none", FromEmail: "service@test.invalid", FromName: "HUIXU QA"}})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SaveSiteSecurity(context.Background(), a.ID, store.SiteSecurity{RegistrationEnabled: true, EmailVerificationEnabled: true, EmailDomains: []string{"@test.invalid", "*.example.com"}}); e != nil {
		t.Fatal(e)
	}
	return s, a, sink
}
func sendCode(t *testing.T, s *store.Store, sink *testutil.MailSink, email, purpose string) string {
	t.Helper()
	ctx := context.Background()
	if e := s.RequestEmailCode(ctx, email, purpose); e != nil {
		t.Fatal(e)
	}
	if worked, e := s.DeliverOneMail(ctx); e != nil || !worked {
		t.Fatalf("send: %v %v", worked, e)
	}
	return sink.Code(t)
}
func TestSiteEmailRegistrationAndReset(t *testing.T) {
	s, a, sink := setupSite(t)
	ctx := context.Background()
	email := "new@test.invalid"
	cfg, e := s.SiteSettings(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if !cfg.Security.Allows("x@sub.example.com") || cfg.Security.Allows("x@evilexample.com") || cfg.Security.Allows("x@example.net") {
		t.Fatal("domain boundary failed")
	}
	for _, role := range []string{"member", "operator"} {
		u := testutil.Member(t, s, a, role+"@test.invalid", role)
		if e = s.SaveSiteSecurity(ctx, u.ID, cfg.Security); !errors.Is(e, store.ErrForbidden) {
			t.Fatal("non-admin modified settings", e)
		}
	}
	code := sendCode(t, s, sink, email, "register")
	if e = s.RequestEmailCode(ctx, email, "register"); !errors.Is(e, store.ErrAuthRate) {
		t.Fatal("resend cooldown missing", e)
	}
	in := store.RegistrationInput{Email: email, Name: "New member", Password: "LongTestPassword123", Code: "bad"}
	if e = s.Register(ctx, in); !errors.Is(e, store.ErrInvalid) {
		t.Fatal("accepted invalid code", e)
	}
	in.Code = code
	if e = s.Register(ctx, in); e != nil {
		t.Fatal(e)
	}
	if e = s.Register(ctx, in); !errors.Is(e, store.ErrInvalid) {
		t.Fatal("code replay accepted", e)
	}
	u, e := s.Login(ctx, email, in.Password)
	if e != nil || u.Role != "member" || u.Balance != 0 || u.ServiceFee != 10000 {
		t.Fatalf("registration privilege/default mismatch: %v", e)
	}
	session, e := s.NewSession(ctx, u.ID)
	if e != nil {
		t.Fatal(e)
	}
	key, e := s.CreateKey(ctx, u.ID, "before-reset")
	if e != nil {
		t.Fatal(e)
	}
	reset := sendCode(t, s, sink, email, "reset")
	// Register proof cannot reset a password, even if the strings coincide.
	if e = s.ResetPasswordByEmail(ctx, email, "xxxxxx", "ChangedPassword123"); !errors.Is(e, store.ErrInvalid) {
		t.Fatal("invalid reset accepted", e)
	}
	if e = s.ResetPasswordByEmail(ctx, email, reset, "ChangedPassword123"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Login(ctx, email, "ChangedPassword123"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Login(ctx, email, in.Password); e == nil {
		t.Fatal("old password still works")
	}
	if _, e = s.Authenticate(ctx, session, false); e == nil {
		t.Fatal("old session still works")
	}
	if _, e = s.Authenticate(ctx, key, true); e == nil {
		t.Fatal("old API key still works")
	}
	if e = s.ResetPasswordByEmail(ctx, email, reset, "AnotherPassword123"); !errors.Is(e, store.ErrInvalid) {
		t.Fatal("reset replay accepted", e)
	}
	logs, _, e := s.MailLogs(ctx, 1, 20)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(logs)
	if strings.Contains(string(raw), code) || strings.Contains(string(raw), reset) {
		t.Fatal("code leaked in logs")
	}
	var plaintext int
	e = s.Pool.QueryRow(ctx, "SELECT count(*) FROM mail_jobs WHERE payload IS NOT NULL").Scan(&plaintext)
	if e != nil || plaintext != 0 {
		t.Fatal("sent payload not erased", e)
	}
	if e = s.RequestEmailCode(ctx, "missing@test.invalid", "reset"); e != nil {
		t.Fatal("unknown user response differs", e)
	}
	if worked, e := s.DeliverOneMail(ctx); e != nil || worked {
		t.Fatal("unknown user caused an email", e)
	}
}
func TestEmailVerificationLimitsAndConfigurationChanges(t *testing.T) {
	s, a, sink := setupSite(t)
	ctx := context.Background()
	email := "limited@test.invalid"
	code := sendCode(t, s, sink, email, "register")
	in := store.RegistrationInput{Email: email, Name: "Test", Password: "LongTestPassword123", Code: "invalid"}
	for i := 0; i < 5; i++ {
		if e := s.Register(ctx, in); !errors.Is(e, store.ErrInvalid) {
			t.Fatal(e)
		}
	}
	in.Code = code
	if e := s.Register(ctx, in); !errors.Is(e, store.ErrInvalid) {
		t.Fatal("sixth attempt allowed", e)
	}
	cfg, _ := s.SiteSettings(ctx)
	cfg.Security.RegistrationEnabled = false
	if e := s.SaveSiteSecurity(ctx, a.ID, cfg.Security); e != nil {
		t.Fatal(e)
	}
	if e := s.Register(ctx, in); !errors.Is(e, store.ErrForbidden) {
		t.Fatal("closed registration allowed", e)
	}
	if e := s.RequestEmailCode(ctx, "next@test.invalid", "register"); !errors.Is(e, store.ErrForbidden) {
		t.Fatal("closed registration sent mail", e)
	}
	cfg.Security.RegistrationEnabled = true
	cfg.Security.EmailVerificationEnabled = false
	if e := s.SaveSiteSecurity(ctx, a.ID, cfg.Security); e != nil {
		t.Fatal(e)
	}
	in.Email = "noverify@test.invalid"
	in.Code = ""
	if e := s.Register(ctx, in); e != nil {
		t.Fatal(e)
	}
	in.Email = "x@evilexample.com"
	if e := s.Register(ctx, in); !errors.Is(e, store.ErrInvalid) {
		t.Fatal("whitelist bypass", e)
	}
	for i := 0; i < 10; i++ {
		if e := s.AuthRate(ctx, "127.0.0.1", "limit@test.invalid", "send"); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.AuthRate(ctx, "127.0.0.1", "limit@test.invalid", "send"); !errors.Is(e, store.ErrAuthRate) {
		t.Fatal("rate limit missing", e)
	}
}
func TestConcurrentRegistrationConsumesOneCode(t *testing.T) {
	s, _, sink := setupSite(t)
	email := "race@test.invalid"
	code := sendCode(t, s, sink, email, "register")
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.Register(ctx, store.RegistrationInput{Email: email, Name: "Race", Password: "LongTestPassword123", Code: code})
		}()
	}
	wg.Wait()
	close(errs)
	success := 0
	for e := range errs {
		if e == nil {
			success++
		} else if !errors.Is(e, store.ErrInvalid) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatalf("expected one registration, got %d", success)
	}
}
func TestSMTPPasswordPrivacyExpiryAndRetry(t *testing.T) {
	s, a, sink := setupSite(t)
	ctx := context.Background()
	mail := store.MailInput{SiteMail: store.SiteMail{Host: sink.Host, Port: sink.Port, Username: "mailer", Encryption: "starttls", FromEmail: "service@test.invalid"}, Password: "smtp-secret-private"}
	if e := s.SaveSiteMail(ctx, a.ID, mail); e != nil {
		t.Fatal(e)
	}
	cfg, e := s.SiteSettings(ctx)
	if e != nil || !cfg.Mail.PasswordConfigured {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(cfg)
	if strings.Contains(string(raw), mail.Password) {
		t.Fatal("password leaked")
	}
	mail.Password = ""
	if e = s.SaveSiteMail(ctx, a.ID, mail); e != nil {
		t.Fatal(e)
	}
	// A plain fake server must not cause downgrade after STARTTLS is required.
	if e = s.QueueTestMail(ctx, a.ID, "sink@test.invalid"); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		if _, e = s.Pool.Exec(ctx, "UPDATE mail_jobs SET next_attempt_at=now()"); e != nil {
			t.Fatal(e)
		}
		if worked, e := s.DeliverOneMail(ctx); e != nil || !worked {
			t.Fatal(worked, e)
		}
	}
	logs, _, e := s.MailLogs(ctx, 1, 20)
	if e != nil || len(logs) != 1 || logs[0].Status != "failed" || logs[0].Attempts != 3 {
		t.Fatalf("retry not bounded: %+v %v", logs, e)
	}
	if len(sink.Messages) != 0 {
		t.Fatal("TLS downgrade sent a message")
	}
	mail.Username = ""
	mail.Encryption = "none"
	mail.ClearPassword = true
	if e = s.SaveSiteMail(ctx, a.ID, mail); e != nil {
		t.Fatal(e)
	}
	if e = s.RequestEmailCode(ctx, "expired@test.invalid", "register"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Pool.Exec(ctx, "UPDATE auth_email_challenges SET expires_at=now()-interval '1 second' WHERE email='expired@test.invalid'"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DeliverOneMail(ctx); e != nil {
		t.Fatal(e)
	}
	if len(sink.Messages) != 0 {
		t.Fatal("expired code delivered")
	}
}

func TestSiteSettingsAndPendingMailPersistAcrossStartup(t *testing.T) {
	s, a, sink := setupSite(t)
	ctx := context.Background()
	cfg, e := s.SiteSettings(ctx)
	if e != nil {
		t.Fatal(e)
	}
	cfg.Features["developer"] = false
	if e = s.SaveSiteFeatures(ctx, a.ID, cfg.Features); e != nil {
		t.Fatal(e)
	}
	// Even an unused relay secret must remain encrypted and survive migration.
	if e = s.SaveSiteMail(ctx, a.ID, store.MailInput{SiteMail: cfg.Mail, Password: "persist-private-smtp"}); e != nil {
		t.Fatal(e)
	}
	if e = s.RequestEmailCode(ctx, "restart@test.invalid", "register"); e != nil {
		t.Fatal(e)
	}
	reopened, e := store.Open(ctx, s.Pool.Config().ConnString(), []byte("0123456789abcdef0123456789abcdef"))
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Pool.Close()
	loaded, e := reopened.SiteSettings(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if loaded.Features["developer"] || !loaded.Security.RegistrationEnabled || !loaded.Mail.PasswordConfigured || len(loaded.Security.EmailDomains) != 2 {
		t.Fatal("startup reset settings")
	}
	if worked, e := reopened.DeliverOneMail(ctx); e != nil || !worked {
		t.Fatal("pending mail lost after startup", worked, e)
	}
	code := sink.Code(t)
	if e = reopened.Register(ctx, store.RegistrationInput{Email: "restart@test.invalid", Name: "Restart", Password: "LongTestPassword123", Code: code}); e != nil {
		t.Fatal("code lost after startup", e)
	}
}

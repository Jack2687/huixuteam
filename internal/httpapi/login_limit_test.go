package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"llm-refund/internal/testutil"
)

func TestLoginLimitIsolatesAccountsBehindSameProxy(t *testing.T) {
	db := testutil.Database(t)
	a := testutil.Admin(t, db)
	u := testutil.Member(t, db, a, "first@test.invalid", "member")
	v := testutil.Member(t, db, a, "second@test.invalid", "member")
	handler := New(db, false)
	request := func(email, password, forwarded string, want int) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"email": email, "password": password})
		r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(string(body)))
		r.RemoteAddr = "172.18.0.1:12345"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Forwarded-For", forwarded)
		r.Header.Set("CF-Connecting-IP", forwarded)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("login %s: got %d want %d", email, w.Code, want)
		}
		return w
	}
	for i := 0; i < 10; i++ {
		request(u.Email, "wrong-password", "198.51.100.1", 401)
	}
	w := request(" FIRST@test.invalid ", "TestPassword-12345", "198.51.100.2", 429)
	if w.Header().Get("Retry-After") == "" || !strings.Contains(w.Body.String(), "此账号") {
		t.Fatal("missing scoped retry guidance")
	}
	// Same reverse-proxy source: other customers and administrators still log in.
	w = request(v.Email, "TestPassword-12345", "198.51.100.1", 200)
	if len(w.Result().Cookies()) != 1 {
		t.Fatal("missing session")
	}
	request(a.Email, "TestPassword-12345", "198.51.100.1", 200)
	// A successful login resets only its own failed attempts, not other accounts.
	for round := 0; round < 2; round++ {
		for i := 0; i < 9; i++ {
			request(v.Email, "wrong-password", "198.51.100.1", 401)
		}
		request(v.Email, "TestPassword-12345", "198.51.100.1", 200)
	}
	request(u.Email, "TestPassword-12345", "198.51.100.3", 429)
}

func TestLoginLimitWindowAndParallelReservations(t *testing.T) {
	s := &Server{attempts: map[string]attempt{}}
	now := time.Now()
	key := loginAttemptKey("USER@example.test")
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.reserveLoginAttempt(key, now) == 0 {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 10 {
		t.Fatal("parallel attempts bypassed limit", accepted.Load())
	}
	if retry := s.reserveLoginAttempt(key, now.Add(14*time.Minute+30*time.Second)); retry != 30 {
		t.Fatal("incorrect remaining window", retry)
	}
	if s.reserveLoginAttempt(key, now.Add(15*time.Minute)) != 0 {
		t.Fatal("window did not expire")
	}
	if loginAttemptKey(" user@EXAMPLE.test ") != key {
		t.Fatal("normalization differs from login")
	}
	if s.reserveLoginAttempt(loginAttemptKey("other@example.test"), now) != 0 {
		t.Fatal("other account blocked")
	}
}

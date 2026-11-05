package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
)

func TestLoginSessionRejectsChangedCredentials(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "login-race@test.invalid", "member")
	ctx := context.Background()
	proof, err := s.Login(ctx, u.Email, "TestPassword-12345")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(proof)
	if strings.Contains(string(raw), "loginHash") || strings.Contains(string(raw), "$2") {
		t.Fatal("login proof leaked")
	}
	if err = s.AdminPassword(ctx, a.ID, u.ID, "Updated-password-123"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.NewLoginSession(ctx, proof); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("stale login was accepted", err)
	}
	if _, err = s.NewLoginSession(ctx, u); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("missing login proof accepted", err)
	}
	proof, err = s.Login(ctx, u.Email, "Updated-password-123")
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.NewLoginSession(ctx, proof)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(ctx, token, false); err != nil {
		t.Fatal(err)
	}
	if err = s.AdminPassword(ctx, a.ID, u.ID, "Another-password-123"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(ctx, token, false); err == nil {
		t.Fatal("issued session survived reset")
	}
}

func TestLoginSessionRejectsRenamedOrDisabledAccount(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	u := testutil.Member(t, s, a, "login-state@test.invalid", "member")
	ctx := context.Background()
	for _, change := range []string{"UPDATE users SET email='renamed@test.invalid' WHERE id=$1", "UPDATE users SET disabled=true WHERE id=$1"} {
		var email string
		if err := s.Pool.QueryRow(ctx, "SELECT email FROM users WHERE id=$1", u.ID).Scan(&email); err != nil {
			t.Fatal(err)
		}
		proof, err := s.Login(ctx, email, "TestPassword-12345")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Pool.Exec(ctx, change, u.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.NewLoginSession(ctx, proof); !errors.Is(err, store.ErrForbidden) {
			t.Fatal("changed account accepted", err)
		}
	}
}

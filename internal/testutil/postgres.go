package testutil

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"llm-refund/internal/store"
	"net/url"
	"os"
	"testing"
)

// Each test owns a fresh schema. Tests never truncate application tables.
func Database(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := "refund_test_" + store.ID()
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	q.Set("search_path", name)
	parsed.RawQuery = q.Encode()
	s, err := store.Open(ctx, parsed.String(), []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Pool.Close()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	return s
}
func Admin(t *testing.T, s *store.Store) store.User {
	t.Helper()
	ctx := context.Background()
	if err := s.Bootstrap(ctx, "admin@test.invalid", "TestPassword-12345", "Test admin"); err != nil {
		t.Fatal(err)
	}
	u, err := s.Login(ctx, "admin@test.invalid", "TestPassword-12345")
	if err != nil {
		t.Fatal(err)
	}
	return u
}
func Member(t *testing.T, s *store.Store, admin store.User, email, role string) store.User {
	t.Helper()
	id, err := s.AdminCreateUser(context.Background(), admin.ID, store.UserInput{Email: email, Name: email, Password: "TestPassword-12345", Role: role, ServiceFee: 3000})
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.User(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

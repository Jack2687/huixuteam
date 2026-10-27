package store_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"llm-refund/internal/store"
)

func setMotherInterval(t *testing.T, s *store.Store, admin store.User, seconds int) {
	t.Helper()
	ctx := context.Background()
	cfg, err := s.RefundSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MotherIntervalSeconds = seconds
	if err = s.SaveRefundSettings(ctx, admin.ID, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := s.RefundSettings(ctx)
	if err != nil || !reflect.DeepEqual(got, cfg) {
		t.Fatal("settings not persisted", got, err)
	}
}

func completeIntervalJob(t *testing.T, s *store.Store, j *store.RefundJob) {
	t.Helper()
	prepareExport(t, s, j)
	for _, step := range []string{"merge_v2_accepted", "removing", "completed"} {
		checkpoint(t, s, j, step)
	}
}

func TestMotherIntervalCompletionExpiryAndConcurrentClaim(t *testing.T) {
	s, a, u := executionFixture(t, 1)
	ctx := context.Background()
	setMotherInterval(t, s, a, 60)
	submitRequests(t, s, u, 3)
	j, err := s.ClaimRefund(ctx)
	if err != nil || j == nil {
		t.Fatal("first request should not wait", err)
	}
	completeIntervalJob(t, s, j)
	for i := 0; i < 3; i++ {
		if next, err := s.ClaimRefund(ctx); err != nil || next != nil {
			t.Fatal("mother reused before interval", next, err)
		}
	}
	var frozen int64
	if err = s.Pool.QueryRow(ctx, "SELECT frozen FROM users WHERE id=$1", u.ID).Scan(&frozen); err != nil || frozen != 6000 {
		t.Fatal("waiting changed frozen credits", frozen, err)
	}
	if _, err = s.Pool.Exec(ctx, "UPDATE refund_execution SET completed_at=now()-interval '61 seconds' WHERE refund_id=$1", j.ID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	claimed := []*store.RefundJob{}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next, err := s.ClaimRefund(ctx)
			if err != nil {
				t.Error(err)
			}
			if next != nil {
				mu.Lock()
				claimed = append(claimed, next)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(claimed) != 1 || claimed[0].Mother.ID != j.Mother.ID {
		t.Fatal("expired interval must allow only one new reservation", len(claimed))
	}
}

func TestMotherIntervalDoesNotBlockOtherMothersOrAcceptedRecovery(t *testing.T) {
	s, a, u := executionFixture(t, 2)
	ctx := context.Background()
	setMotherInterval(t, s, a, 3600)
	submitRequests(t, s, u, 3)
	first, err := s.ClaimRefund(ctx)
	if err != nil || first == nil {
		t.Fatal(err)
	}
	completeIntervalJob(t, s, first)
	other, err := s.ClaimRefund(ctx)
	if err != nil || other == nil || other.Mother.ID == first.Mother.ID {
		t.Fatal("another mother should continue", err)
	}
	// The same mother can still recover its current accepted request even when
	// increasing the interval makes its previous completion fall within it.
	setMotherInterval(t, s, a, 0)
	current, err := s.ClaimRefund(ctx)
	if err != nil || current == nil || current.Mother.ID != first.Mother.ID {
		t.Fatal(err)
	}
	prepareExport(t, s, current)
	checkpoint(t, s, current, "merge_v2_accepted")
	setMotherInterval(t, s, a, 3600)
	if _, err = s.Pool.Exec(ctx, "UPDATE refund_execution SET lease_until=now()-interval '1 second' WHERE refund_id=$1", current.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.ClaimRefund(ctx)
	if err != nil || recovered == nil || recovered.ID != current.ID || recovered.Step != "accepted" {
		t.Fatal("interval blocked accepted recovery", err)
	}
}

func TestMotherIntervalLiveSettingsAndValidation(t *testing.T) {
	s, a, u := executionFixture(t, 1)
	ctx := context.Background()
	cfg, err := s.RefundSettings(ctx)
	if err != nil || cfg.MotherIntervalSeconds != 0 {
		t.Fatal("legacy default changed", cfg, err)
	}
	for _, invalid := range []int{-1, 86401} {
		cfg.MotherIntervalSeconds = invalid
		if err = s.SaveRefundSettings(ctx, a.ID, cfg); !errors.Is(err, store.ErrInvalid) {
			t.Fatal("invalid interval saved", invalid, err)
		}
	}
	setMotherInterval(t, s, a, 60)
	submitRequests(t, s, u, 2)
	j, err := s.ClaimRefund(ctx)
	if err != nil || j == nil {
		t.Fatal(err)
	}
	completeIntervalJob(t, s, j)
	if _, err = s.Pool.Exec(ctx, "UPDATE refund_execution SET completed_at=now()-interval '30 seconds' WHERE refund_id=$1", j.ID); err != nil {
		t.Fatal(err)
	}
	if next, err := s.ClaimRefund(ctx); err != nil || next != nil {
		t.Fatal("claimed before 60 seconds", err)
	}
	setMotherInterval(t, s, a, 10)
	if next, err := s.ClaimRefund(ctx); err != nil || next == nil {
		t.Fatal("new interval not used immediately", err)
	}
}

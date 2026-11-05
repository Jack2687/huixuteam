package httpapi_test

import (
	"context"
	"io"
	"llm-refund/internal/httpapi"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCodeToggleRequiresExplicitBoolean(t *testing.T) {
	s := testutil.Database(t)
	a := testutil.Admin(t, s)
	ctx := context.Background()
	codes, err := s.GenerateCodes(ctx, a.ID, store.CodeInput{Count: 1, Amount: 1000})
	if err != nil {
		t.Fatal(err)
	}
	var id string
	if err = s.Pool.QueryRow(ctx, "SELECT id FROM redeem_codes WHERE code_hash=$1", store.Hash(codes[0])).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err = s.DisableCode(ctx, a.ID, id, true); err != nil {
		t.Fatal(err)
	}
	session, err := s.NewSession(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.New(s, false))
	defer server.Close()
	for _, body := range []string{`{}`, `null`, `{"disabled":null}`, `{"disabled":"false"}`} {
		req, err := http.NewRequest("PATCH", server.URL+"/api/admin/codes/"+id, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "refund_session", Value: session})
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != 400 {
			t.Fatalf("invalid body returned %d", res.StatusCode)
		}
		var disabled bool
		if err = s.Pool.QueryRow(ctx, "SELECT disabled FROM redeem_codes WHERE id=$1", id).Scan(&disabled); err != nil || !disabled {
			t.Fatal("invalid body enabled code", err)
		}
	}
}

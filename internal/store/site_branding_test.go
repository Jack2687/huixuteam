package store_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
)

func TestBrandIconValidationPersistenceAndIsolation(t *testing.T) {
	s := testutil.Database(t)
	admin := testutil.Admin(t, s)
	member := testutil.Member(t, s, admin, "brand@test.invalid", "member")
	ctx := context.Background()
	before, err := s.SiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	data, mime, err := s.BrandIcon(ctx)
	if err != nil || mime != "image/svg+xml" || !bytes.Equal(data, store.DefaultBrandIcon) || before.Branding.Customized {
		t.Fatal("invalid default icon", err)
	}
	if _, err := s.SaveBrandIcon(ctx, member.ID, data, false); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("member changed icon", err)
	}
	for _, raw := range [][]byte{
		nil, []byte("not an image"), bytes.Repeat([]byte{'a'}, store.MaxBrandIconBytes+1),
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"></svg>`),
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><image href="https://example.com/x"/></svg>`),
		[]byte(`<svg><path fill="url(https://example.com/a)"/></svg>`),
	} {
		if _, err := s.SaveBrandIcon(ctx, admin.ID, raw, false); !errors.Is(err, store.ErrInvalid) {
			t.Fatal("invalid icon accepted", err)
		}
	}
	var oversized bytes.Buffer
	if err := png.Encode(&oversized, image.NewGray(image.Rect(0, 0, 2049, 1))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveBrandIcon(ctx, admin.ID, oversized.Bytes(), false); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("oversized dimensions accepted", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	var raster bytes.Buffer
	if err := jpeg.Encode(&raster, img, nil); err != nil {
		t.Fatal(err)
	}
	raster.WriteString("private-upload-metadata")
	custom, err := s.SaveBrandIcon(ctx, admin.ID, raster.Bytes(), false)
	if err != nil || !custom.Customized || custom.IconURL == before.Branding.IconURL {
		t.Fatal("custom icon not saved", err)
	}
	data, mime, err = s.BrandIcon(ctx)
	if err != nil || mime != "image/png" || bytes.Contains(data, []byte("private-upload-metadata")) {
		t.Fatal("raster not normalized", err)
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	// A normal application startup must not reset a saved icon.
	reopened, err := store.Open(ctx, s.Pool.Config().ConnString(), []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Pool.Close()
	after, err := reopened.SiteSettings(ctx)
	if err != nil || after.Branding != custom || len(after.Features) != len(before.Features) || after.Security.RegistrationEnabled != before.Security.RegistrationEnabled {
		t.Fatal("restart lost branding or changed settings", err)
	}
	if _, err = s.SaveBrandIcon(ctx, admin.ID, store.DefaultBrandIcon, false); err != nil {
		t.Fatal("designed SVG cannot be uploaded", err)
	}
	if _, err = s.SaveBrandIcon(ctx, member.ID, nil, true); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("member reset branding", err)
	}
	restored, err := s.SaveBrandIcon(ctx, admin.ID, nil, true)
	if err != nil || restored != before.Branding {
		t.Fatal("default reset failed", err)
	}
	var audit string
	if err = s.Pool.QueryRow(ctx, "SELECT string_agg(metadata::text,'') FROM audit_logs WHERE action='site.branding'").Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(audit, "private-upload-metadata") || strings.Contains(audit, "<svg") {
		t.Fatal("icon data leaked to audit")
	}
}

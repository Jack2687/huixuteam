package store_test

import (
	"context"
	"errors"
	"llm-refund/internal/store"
	"llm-refund/internal/testutil"
	"reflect"
	"strings"
	"testing"
)

func TestCustomMenuValidationAndPersistence(t *testing.T) {
	s := testutil.Database(t)
	admin := testutil.Admin(t, s)
	ctx := context.Background()
	user := testutil.Member(t, s, admin, "menus@test.invalid", "member")
	base := store.CustomMenu{ID: "support", Label: "帮助中心", URL: "https://example.com/help?lang=zh#intro", Visibility: "user", Enabled: true, OpenInNewTab: true, HideOpenButton: true, IconSVG: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M0 0L24 24" stroke="#333"/></svg>`}
	if err := s.SaveCustomMenus(ctx, user.ID, []store.CustomMenu{base}); !errors.Is(err, store.ErrForbidden) {
		t.Fatal(err)
	}
	for _, badURL := range []string{"javascript:alert(1)", "data:text/html,x", "//example.com", "/workspace/admin", "https://u:p@example.com", "https://example.com\\evil", "https://example.com/\r\nX-Test: bad"} {
		v := base
		v.URL = badURL
		if err := s.SaveCustomMenus(ctx, admin.ID, []store.CustomMenu{v}); !errors.Is(err, store.ErrInvalid) {
			t.Fatalf("URL accepted: %q: %v", badURL, err)
		}
	}
	for _, svg := range []string{`<svg><script>alert(1)</script></svg>`, `<svg onload="alert(1)"></svg>`, `<svg><foreignObject/></svg>`, `<svg><image href="https://example.com"/></svg>`, `<svg><path fill="url(https://example.com)"/></svg>`, `<!DOCTYPE svg [<!ENTITY x "a">]><svg/>`, `<svg style="display:block"/>`, `<svg/><svg/>`, `<svg><g></svg>`, strings.Repeat(" ", 5) + strings.Repeat("x", 16385)} {
		v := base
		v.IconSVG = svg
		if err := s.SaveCustomMenus(ctx, admin.ID, []store.CustomMenu{v}); !errors.Is(err, store.ErrInvalid) {
			t.Fatalf("unsafe SVG accepted: %v", err)
		}
	}
	if err := s.SaveCustomMenus(ctx, admin.ID, []store.CustomMenu{base, base}); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("duplicate id accepted", err)
	}
	wrong := base
	wrong.Visibility = "operator"
	if err := s.SaveCustomMenus(ctx, admin.ID, []store.CustomMenu{wrong}); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("unknown role accepted", err)
	}
	private := base
	private.ID = "private"
	private.Visibility = "admin"
	private.Label = "内部入口"
	if err := s.SaveCustomMenus(ctx, admin.ID, []store.CustomMenu{private, base}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CustomMenu(ctx, user, "private"); !errors.Is(err, store.ErrForbidden) {
		t.Fatal(err)
	}
	settings, err := s.SiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.CustomMenus[0].ID != "private" || settings.CustomMenus[1].SortOrder != 1 {
		t.Fatal("order lost")
	}
	// Startup migration must retain menu order, SVG and both opening preferences.
	reopened, err := store.Open(ctx, s.Pool.Config().ConnString(), []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Pool.Close()
	loaded, err := reopened.SiteSettings(ctx)
	if err != nil || !reflect.DeepEqual(settings.CustomMenus, loaded.CustomMenus) {
		t.Fatal("menus changed after migration", err)
	}
	if err = s.SaveCustomMenus(ctx, admin.ID, []store.CustomMenu{base, private}); err != nil {
		t.Fatal(err)
	}
	settings, _ = s.SiteSettings(ctx)
	if settings.CustomMenus[0].ID != "support" {
		t.Fatal("reorder lost")
	}
	if err = s.SaveCustomMenus(ctx, admin.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CustomMenu(ctx, user, "support"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("deleted menu accessible", err)
	}
}

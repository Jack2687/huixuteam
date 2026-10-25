package webui

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A missing lazy-loaded dependency leaves a page blank even when index loads.
func TestAllBuiltAssetsAreEmbedded(t *testing.T) {
	err := filepath.WalkDir("static", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		built, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		embedded, err := Static.ReadFile(filepath.ToSlash(path))
		if err != nil {
			t.Errorf("built asset missing from binary: %s: %v", path, err)
			return nil
		}
		if !bytes.Equal(built, embedded) {
			t.Errorf("embedded asset differs from build: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Applies equally to public and authenticated administrator bundles.
func TestNoPrivateProtocolInFrontend(t *testing.T) {
	err := fs.WalkDir(Static, "static", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		for _, suffix := range []string{".map", ".py", ".go", ".sql", ".env", ".md", ".toml", ".yaml", ".yml"} {
			if strings.HasSuffix(path, suffix) {
				t.Errorf("private source/config embedded: %s", path)
			}
		}
		body, err := Static.ReadFile(path)
		if err != nil {
			return err
		}
		for _, secret := range []string{"export_and_deactivate", "backend-api/accounts", "auth.openai.com", "chatgpt.com/api/auth", "chatgpt-account-id", "openai-sentinel", "workspace/select", "mfa/verify", "merge_v2", "child_user_id", "accepted_at", "invalid_workspace_selected", "curl_cffi", "submit_export", "sourceMappingURL", "登录子号", "导出停用", "移出团队"} {
			if bytes.Contains(body, []byte(secret)) {
				t.Errorf("private protocol %q found in %s", secret, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

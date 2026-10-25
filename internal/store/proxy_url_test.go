package store

import (
	"net/url"
	"strings"
	"testing"
)

func TestNormalizeProxyURL(t *testing.T) {
	for _, tc := range []struct{ name, input, scheme, host, user, password string }{
		{"four fields", " 192.0.2.10:8858:fixture-user:fixture-pass ", "http", "192.0.2.10:8858", "fixture-user", "fixture-pass"},
		{"special characters", "proxy.invalid:8858:user@tenant:p@ss:/?#%word", "http", "proxy.invalid:8858", "user@tenant", "p@ss:/?#%word"},
		{"url in password", "proxy.invalid:8858:user:p://secret", "http", "proxy.invalid:8858", "user", "p://secret"},
		{"ipv6", "[2001:db8::1]:8858:user:pass", "http", "[2001:db8::1]:8858", "user", "pass"},
		{"no credentials", "proxy.invalid:8858", "http", "proxy.invalid:8858", "", ""},
		{"http", "http://user:pass@proxy.invalid:8080", "http", "proxy.invalid:8080", "user", "pass"},
		{"https", "https://user:pass@proxy.invalid", "https", "proxy.invalid", "user", "pass"},
		{"socks", "socks5h://user:p%40ss@proxy.invalid:1080", "socks5h", "proxy.invalid:1080", "user", "p@ss"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeProxyURL(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(got)
			if err != nil {
				t.Fatal(err)
			}
			password, _ := u.User.Password()
			if u.Scheme != tc.scheme || u.Host != tc.host || u.User.Username() != tc.user || password != tc.password {
				t.Fatal("proxy connection fields changed")
			}
			again, err := normalizeProxyURL(got)
			if err != nil || again != got {
				t.Fatal("normalized URL is not stable")
			}
		})
	}
	for _, input := range []string{"", "host:abc:user:private-secret", "host:0:user:private-secret", "host:65536:user:private-secret", "host:8080:user", "host:8080::private-secret", "host:8080:user:", "http://host:0", "http://host:", "http://host/path", "http://host?query=1", "http://host?", "ftp://host:21", "http://user:private-secret@/", "host:8080:user:private-secret\nextra"} {
		if _, err := normalizeProxyURL(input); err == nil {
			t.Fatal("invalid proxy accepted")
		} else if strings.Contains(err.Error(), "private-secret") {
			t.Fatal("validation error exposed credentials")
		}
	}
}

package httpapi

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUnexpectedErrorDoesNotExposeRawDataInResponseOrLog(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	r := httptest.NewRecorder()
	(&Server{}).result(r, nil, errors.New("private-database-value export_and_deactivate Bearer private-token"))
	if r.Code != 500 {
		t.Fatal("unexpected response status")
	}
	for _, word := range []string{"private-database-value", "export_and_deactivate", "private-token"} {
		if strings.Contains(r.Body.String()+logs.String(), word) {
			t.Fatal("raw error exposed")
		}
	}
	if !strings.Contains(logs.String(), "database operation failed") {
		t.Fatal("missing generic diagnostic")
	}
}

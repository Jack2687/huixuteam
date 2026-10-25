// Package refundprovider is the only integration seam for the real refund flow.
// Implement Submit and Poll here when the upstream protocol is provided.
package refundprovider

import (
	"context"
	"errors"
)

var ErrNotConfigured = errors.New("refund provider is not configured")

type Account struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	TOTPSecret string `json:"totp_secret"`
}

type Result struct {
	Reference string
	Status    string
	Message   string
}

type Provider interface {
	Submit(context.Context, string, Account) (Result, error)
	Poll(context.Context, string) (Result, error)
}

// Unconfigured intentionally performs no network calls and never reports success.
type Unconfigured struct{}

func (Unconfigured) Submit(context.Context, string, Account) (Result, error) {
	return Result{}, ErrNotConfigured
}
func (Unconfigured) Poll(context.Context, string) (Result, error) {
	return Result{}, ErrNotConfigured
}

package httpapi

import (
	"strings"
	"time"
)

func loginAttemptKey(email string) string {
	// Match Store.Login normalization. A reverse proxy or shared network must
	// never put every account in the same login bucket. Forwarded IP headers are
	// deliberately irrelevant, so forging them cannot bypass account protection.
	return "login:" + strings.ToLower(strings.TrimSpace(email))
}

func (s *Server) reserveLoginAttempt(key string, now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, a := range s.attempts {
		if !now.Before(a.until) {
			delete(s.attempts, k)
		}
	}
	a := s.attempts[key]
	if a.count >= 10 {
		return int((a.until.Sub(now) + time.Second - 1) / time.Second)
	}
	if a.count == 0 {
		a.until = now.Add(15 * time.Minute)
	}
	// Reserve before password verification so parallel attempts share the limit.
	// Successful authentication clears only this account's bucket.
	a.count++
	s.attempts[key] = a
	return 0
}

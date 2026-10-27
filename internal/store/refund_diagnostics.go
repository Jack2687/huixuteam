package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Strict enums are deliberate: upstream-controlled text never becomes a log.
type RefundEvidence struct {
	ProxyHTTPStatus   int    `json:"proxy_http_status,omitempty"`
	Stage             string `json:"stage,omitempty"`
	Side              string `json:"side,omitempty"`
	Event             string `json:"event,omitempty"`
	Reason            string `json:"reason,omitempty"`
	Exception         string `json:"exception,omitempty"`
	UpstreamCode      string `json:"upstream_code,omitempty"`
	BodyKind          string `json:"body_kind,omitempty"`
	Source            string `json:"source,omitempty"`
	Line              int    `json:"line,omitempty"`
	Sequence          int    `json:"sequence,omitempty"`
	TransportAttempt  int    `json:"transport_attempt,omitempty"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
	Challenge         bool   `json:"challenge,omitempty"`
	ExitCode          int    `json:"exit_code,omitempty"`
}

const evidenceReasons = "csrf_or_risk_blocked chatgpt_signin_failed oauth_session_missing chatgpt_rate_limited totp_secret_missing totp_secret_invalid totp_factor_missing totp_dep_missing phone_verification_skipped passkey_or_challenge account_deactivated account_deleted account_banned account_disabled account_suspended incorrect_password invalid_grant invalid_workspace_selected invalid_session invalid_auth_step token_expired rate_limit_exceeded invalid_totp_code invalid_otp unknown_error network_error process_exit process_start process_io deadline cancelled checkpoint_failed malformed_protocol missing_result evidence_limit other"

func evidenceEnum(value, values string) string {
	for _, allowed := range strings.Fields(values) {
		if value == allowed {
			return value
		}
	}
	return ""
}
func evidenceNumber(value, maximum int) int {
	if value < 0 || value > maximum {
		return 0
	}
	return value
}
func (v RefundEvidence) Sanitized() *RefundEvidence {
	v.ProxyHTTPStatus = evidenceNumber(v.ProxyHTTPStatus, 599)
	v.Stage = evidenceEnum(v.Stage, "login csrf signin authorize identifier password mfa_challenge mfa_verify workspace callback session sentinel email_verification personal_check members_read invites_read invite_send member_remove mother_operation export runtime checkpoint")
	v.Side = evidenceEnum(v.Side, "child mother worker")
	v.Event = evidenceEnum(v.Event, "start response error failure truncated")
	v.Reason = evidenceEnum(v.Reason, evidenceReasons)
	v.UpstreamCode = evidenceEnum(v.UpstreamCode, evidenceReasons)
	v.Exception = evidenceEnum(v.Exception, "RuntimeError ValueError KeyError TypeError AttributeError ImportError ModuleNotFoundError TimeoutError OSError ConnectionError LoginFlowError DeadAccountError NeedsVerification UpstreamFailure VerificationUnavailable RequestsError ProxyError SSLError DNSError ConnectTimeout ReadTimeout ConnectionResetError JSONDecodeError other")
	v.BodyKind = evidenceEnum(v.BodyKind, "json html text")
	v.Source = evidenceEnum(v.Source, "refund_runner.py refund_recovery.py diagnostics.py adapter.py upstream.py merge_v2.py bridge.py tls.py sentinel_vm.py")
	v.Line = evidenceNumber(v.Line, 100000)
	v.Sequence = evidenceNumber(v.Sequence, 128)
	v.TransportAttempt = evidenceNumber(v.TransportAttempt, 10)
	v.RetryAfterSeconds = evidenceNumber(v.RetryAfterSeconds, 86400)
	v.ExitCode = evidenceNumber(v.ExitCode, 255)
	return &v
}

type RefundDiagnosticContext struct {
	Attempt       string `json:"attempt"`
	Step          string `json:"step"`
	Mode          string `json:"mode"`
	Retry         int    `json:"retry"`
	Verify        int    `json:"verify"`
	Cleanup       int    `json:"cleanup"`
	MotherID      string `json:"mother_id"`
	ChildProxyID  string `json:"child_proxy_id"`
	MotherProxyID string `json:"mother_proxy_id"`
}

// Append-only telemetry uses no business locks and never changes status, leases,
// retry policy, progress or money. It may arrive after the job has completed.
func (s *Store) AppendRefundDiagnostics(ctx context.Context, j *RefundJob, batch []RefundDiagnostic) error {
	if len(batch) == 0 {
		return nil
	}
	if len(batch) > 16 {
		return ErrInvalid
	}
	type entry struct {
		RefundDiagnostic
		Context RefundDiagnosticContext `json:"context"`
	}
	metadata := j.DiagnosticContext
	digest := sha256.Sum256([]byte(j.LeaseToken))
	metadata.Attempt = hex.EncodeToString(digest[:16])
	rows := make([]entry, 0, len(batch))
	for _, d := range batch {
		d.Code = "protocol_trace"
		rows = append(rows, entry{d.Sanitized(), metadata})
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO refund_execution_diagnostics(refund_id,diagnostic)
 SELECT $1,item FROM jsonb_array_elements($2::jsonb) item`, j.ID, rows)
	return err
}

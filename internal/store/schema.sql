CREATE TABLE IF NOT EXISTS users (
 id TEXT PRIMARY KEY, email TEXT UNIQUE NOT NULL, name TEXT NOT NULL,
 password_hash TEXT NOT NULL, balance BIGINT NOT NULL DEFAULT 0 CHECK(balance >= 0),
 frozen BIGINT NOT NULL DEFAULT 0 CHECK(frozen >= 0 AND frozen <= balance),
 service_fee BIGINT NOT NULL DEFAULT 3000 CHECK(service_fee >= 0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE users ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'member' CHECK(role IN ('admin','operator','member'));
ALTER TABLE users ADD COLUMN IF NOT EXISTS disabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
CREATE TABLE IF NOT EXISTS sessions (
 token_hash TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_expiry ON sessions(expires_at);
CREATE TABLE IF NOT EXISTS api_keys (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id), name TEXT NOT NULL,
 token_hash TEXT UNIQUE NOT NULL, prefix TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 last_used_at TIMESTAMPTZ, revoked_at TIMESTAMPTZ
);
CREATE TABLE IF NOT EXISTS refunds (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id), email TEXT NOT NULL,
 credentials BYTEA NOT NULL, status TEXT NOT NULL DEFAULT 'queued',
 fee BIGINT NOT NULL CHECK(fee >= 0), fee_status TEXT NOT NULL DEFAULT 'frozen',
 message TEXT NOT NULL DEFAULT '申请已保存，等待退款服务接入。',
 provider_reference TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK(status IN ('queued','processing','verifying','succeeded','attention','banned','failed','cancelled')),
 CHECK(fee_status IN ('frozen','settled','released'))
);
CREATE INDEX IF NOT EXISTS refunds_user_time ON refunds(user_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS refunds_active_account ON refunds(user_id,lower(email))
 WHERE status IN ('queued','processing','verifying','attention');
CREATE TABLE IF NOT EXISTS refund_events (
 id BIGSERIAL PRIMARY KEY, refund_id TEXT NOT NULL REFERENCES refunds(id),
 message TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE refund_events ADD COLUMN IF NOT EXISTS internal BOOLEAN NOT NULL DEFAULT false;
CREATE TABLE IF NOT EXISTS ledger (
 id BIGSERIAL PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id), refund_id TEXT REFERENCES refunds(id),
 kind TEXT NOT NULL CHECK(kind IN ('credit','debit','freeze','release','settlement','redeem')),
 amount BIGINT NOT NULL, balance_after BIGINT NOT NULL, note TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS ledger_user_time ON ledger(user_id, id DESC);
CREATE UNIQUE INDEX IF NOT EXISTS ledger_settle_once ON ledger(refund_id) WHERE kind='settlement';
CREATE TABLE IF NOT EXISTS idempotency (
 user_id TEXT NOT NULL REFERENCES users(id), key TEXT NOT NULL, request_hash TEXT NOT NULL,
 refund_ids JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(user_id,key)
);
CREATE TABLE IF NOT EXISTS notifications (
 id BIGSERIAL PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id),
 message TEXT NOT NULL, read_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS notifications_user ON notifications(user_id, id DESC);
ALTER TABLE ledger DROP CONSTRAINT IF EXISTS ledger_kind_check;
ALTER TABLE ledger ADD CONSTRAINT ledger_kind_check CHECK(kind IN ('credit','debit','freeze','release','settlement','redeem'));
CREATE TABLE IF NOT EXISTS audit_logs (
 id BIGSERIAL PRIMARY KEY, actor_id TEXT NOT NULL REFERENCES users(id), action TEXT NOT NULL,
 target_id TEXT NOT NULL, metadata JSONB NOT NULL DEFAULT '{}', created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS admin_idempotency (
 actor_id TEXT NOT NULL REFERENCES users(id), key TEXT NOT NULL, request_hash TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(actor_id,key)
);
CREATE TABLE IF NOT EXISTS redeem_codes (
 id TEXT PRIMARY KEY, code_hash TEXT NOT NULL UNIQUE, prefix TEXT NOT NULL,
 amount BIGINT NOT NULL CHECK(amount>0), max_uses INT NOT NULL CHECK(max_uses>0),
 used_count INT NOT NULL DEFAULT 0 CHECK(used_count>=0 AND used_count<=max_uses),
 expires_at TIMESTAMPTZ, disabled BOOLEAN NOT NULL DEFAULT false, note TEXT NOT NULL DEFAULT '',
 created_by TEXT NOT NULL REFERENCES users(id), created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS redemptions (
 id BIGSERIAL PRIMARY KEY, code_id TEXT NOT NULL REFERENCES redeem_codes(id),
 user_id TEXT NOT NULL REFERENCES users(id), amount BIGINT NOT NULL CHECK(amount>0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(code_id,user_id)
);

CREATE TABLE IF NOT EXISTS refund_settings (
 id BOOLEAN PRIMARY KEY DEFAULT true CHECK(id), enabled BOOLEAN NOT NULL DEFAULT false,
 max_parallel INT NOT NULL DEFAULT 2 CHECK(max_parallel BETWEEN 1 AND 8),
 login_attempts INT NOT NULL DEFAULT 2 CHECK(login_attempts BETWEEN 1 AND 3),
 retry_seconds INT NOT NULL DEFAULT 30 CHECK(retry_seconds BETWEEN 5 AND 300),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO refund_settings(id) VALUES(true) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS refund_proxies (
 id TEXT PRIMARY KEY, name TEXT NOT NULL, secret BYTEA NOT NULL, enabled BOOLEAN NOT NULL DEFAULT true,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS refund_mothers (
 id TEXT PRIMARY KEY, name TEXT NOT NULL, email TEXT NOT NULL UNIQUE, team_id TEXT NOT NULL UNIQUE,
 secret BYTEA NOT NULL, proxy_id TEXT NOT NULL REFERENCES refund_proxies(id),
 seat_type TEXT NOT NULL DEFAULT 'default' CHECK(seat_type IN ('default','prolite')),
 enabled BOOLEAN NOT NULL DEFAULT true, updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS refund_execution (
 refund_id TEXT PRIMARY KEY REFERENCES refunds(id), mother_id TEXT NOT NULL REFERENCES refund_mothers(id),
 step TEXT NOT NULL DEFAULT 'assigned' CHECK(step IN ('assigned','inviting','invited','login','ready','submitting','accepted','removing','completed','failed')),
 child_user_id TEXT NOT NULL DEFAULT '', accepted_at TIMESTAMPTZ, completed_at TIMESTAMPTZ,
 lease_token TEXT NOT NULL DEFAULT '', lease_until TIMESTAMPTZ,
 released BOOLEAN NOT NULL DEFAULT false, updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- A reservation survives worker crashes and attention states. One mother = one job.
CREATE UNIQUE INDEX IF NOT EXISTS refund_execution_mother_serial ON refund_execution(mother_id) WHERE NOT released;
ALTER TABLE refund_execution ADD COLUMN IF NOT EXISTS progress_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE refunds ALTER COLUMN message SET DEFAULT '申请已受理';

CREATE TABLE IF NOT EXISTS announcements (
 id TEXT PRIMARY KEY, title TEXT NOT NULL, content TEXT NOT NULL,
 category TEXT NOT NULL CHECK(category IN ('notice','update','maintenance')),
 published BOOLEAN NOT NULL DEFAULT false, pinned BOOLEAN NOT NULL DEFAULT false,
 created_by TEXT NOT NULL REFERENCES users(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 published_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS announcements_feed ON announcements(pinned DESC,published_at DESC) WHERE published;

ALTER TABLE refund_settings ADD COLUMN IF NOT EXISTS child_proxy_id TEXT REFERENCES refund_proxies(id);
ALTER TABLE refund_execution ADD COLUMN IF NOT EXISTS child_proxy_id TEXT REFERENCES refund_proxies(id);
ALTER TABLE refund_mothers ADD COLUMN IF NOT EXISTS refresh_secret BYTEA;
ALTER TABLE refund_mothers ADD COLUMN IF NOT EXISTS plan_type TEXT NOT NULL DEFAULT '';
ALTER TABLE refund_mothers ADD COLUMN IF NOT EXISTS token_expires_at TIMESTAMPTZ;

ALTER TABLE redeem_codes ADD COLUMN IF NOT EXISTS code_secret BYTEA;
ALTER TABLE redeem_codes ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

ALTER TABLE refund_mothers ADD COLUMN IF NOT EXISTS check_json JSONB;
ALTER TABLE refund_proxies ADD COLUMN IF NOT EXISTS check_json JSONB;

ALTER TABLE refund_settings ADD COLUMN IF NOT EXISTS mother_interval_seconds INTEGER NOT NULL DEFAULT 0 CHECK(mother_interval_seconds BETWEEN 0 AND 86400);
CREATE INDEX IF NOT EXISTS refund_execution_mother_completed ON refund_execution(mother_id,completed_at DESC) WHERE completed_at IS NOT NULL;

-- Explicit maintenance operation only; never run by server startup.
-- Stop the application after draining active work and take a verified backup.
-- On the SAME connection, SET huixu.allow_finance_rescale = '20261105';
BEGIN;
SET LOCAL lock_timeout = '10s';
SET LOCAL statement_timeout = '120s';
SELECT pg_advisory_xact_lock(718374029);
CREATE TABLE IF NOT EXISTS finance_data_migrations (
 id TEXT PRIMARY KEY, factor INTEGER NOT NULL, summary JSONB NOT NULL,
 applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
DO $migration$
DECLARE
 before_totals JSONB;
 after_totals JSONB;
 actor TEXT;
BEGIN
 IF EXISTS(SELECT 1 FROM finance_data_migrations WHERE id='history-x10-20261105') THEN
  RAISE NOTICE 'Historical finance x10 already applied; nothing changed';
  RETURN;
 END IF;
 IF current_setting('huixu.allow_finance_rescale',true) IS DISTINCT FROM '20261105' THEN
  RAISE EXCEPTION 'Explicit maintenance opt-in required';
 END IF;
 LOCK TABLE users,refunds,ledger,redeem_codes,redemptions IN ACCESS EXCLUSIVE MODE;
 IF EXISTS(SELECT 1 FROM refunds WHERE status IN ('processing','verifying'))
 OR EXISTS(SELECT 1 FROM refund_execution WHERE NOT released AND lease_until>now()) THEN
  RAISE EXCEPTION 'Active requests must finish before migration';
 END IF;
 IF EXISTS(SELECT 1 FROM users u WHERE frozen<>(SELECT COALESCE(sum(fee),0) FROM refunds r WHERE r.user_id=u.id AND fee_status='frozen')) THEN
  RAISE EXCEPTION 'Existing frozen balance mismatch; no changes applied';
 END IF;
 IF EXISTS(SELECT 1 FROM refunds r WHERE fee_status='settled' AND
  ((SELECT count(*) FROM ledger l WHERE l.refund_id=r.id AND l.kind='settlement')<>1 OR
   (SELECT amount FROM ledger l WHERE l.refund_id=r.id AND l.kind='settlement')<>-r.fee)) THEN
  RAISE EXCEPTION 'Existing settlement mismatch; no changes applied';
 END IF;
 IF EXISTS(SELECT 1 FROM ledger l JOIN refunds r ON r.id=l.refund_id WHERE l.kind='settlement' AND r.fee_status<>'settled') THEN
  RAISE EXCEPTION 'Unexpected settlement on an unsettled order; no changes applied';
 END IF;
 SELECT id INTO STRICT actor FROM users WHERE role='admin' ORDER BY created_at,id LIMIT 1;
 CREATE TEMP TABLE finance_old_users ON COMMIT DROP AS SELECT id,balance,frozen,service_fee FROM users;
 CREATE TEMP TABLE finance_old_refunds ON COMMIT DROP AS SELECT id,fee FROM refunds;
 CREATE TEMP TABLE finance_old_ledger ON COMMIT DROP AS SELECT id,amount,balance_after FROM ledger;
 CREATE TEMP TABLE finance_old_codes ON COMMIT DROP AS SELECT id,amount FROM redeem_codes;
 CREATE TEMP TABLE finance_old_redemptions ON COMMIT DROP AS SELECT id,amount FROM redemptions;
 -- Keep every individual value exactly representable by existing JS clients.
 IF EXISTS(SELECT 1 FROM (
  SELECT balance AS amount FROM users UNION ALL SELECT frozen FROM users UNION ALL SELECT service_fee FROM users
  UNION ALL SELECT fee FROM refunds UNION ALL SELECT amount FROM ledger UNION ALL SELECT balance_after FROM ledger
  UNION ALL SELECT amount FROM redeem_codes UNION ALL SELECT amount FROM redemptions
 ) v WHERE abs(amount::numeric)>900719925474099) THEN
  RAISE EXCEPTION 'Scaled amount would exceed the existing safe integer range';
 END IF;
 SELECT jsonb_build_object(
  'users',(SELECT jsonb_build_object('rows',count(*),'balance',COALESCE(sum(balance),0),'frozen',COALESCE(sum(frozen),0),'service_fee',COALESCE(sum(service_fee),0)) FROM users),
  'refunds',(SELECT jsonb_build_object('rows',count(*),'fee',COALESCE(sum(fee),0)) FROM refunds),
  'ledger',(SELECT jsonb_build_object('rows',count(*),'amount',COALESCE(sum(amount),0),'balance_after',COALESCE(sum(balance_after),0)) FROM ledger),
  'redeem_codes',(SELECT jsonb_build_object('rows',count(*),'amount',COALESCE(sum(amount),0)) FROM redeem_codes),
  'redemptions',(SELECT jsonb_build_object('rows',count(*),'amount',COALESCE(sum(amount),0)) FROM redemptions)
 ) INTO before_totals;
 UPDATE users SET balance=balance*10,frozen=frozen*10,service_fee=service_fee*10;
 UPDATE refunds SET fee=fee*10;
 UPDATE ledger SET amount=amount*10,balance_after=balance_after*10;
 UPDATE redeem_codes SET amount=amount*10;
 UPDATE redemptions SET amount=amount*10;
 IF EXISTS(SELECT 1 FROM users n FULL JOIN finance_old_users o USING(id) WHERE n.id IS NULL OR o.id IS NULL OR (n.balance,n.frozen,n.service_fee) IS DISTINCT FROM (o.balance*10,o.frozen*10,o.service_fee*10))
 OR EXISTS(SELECT 1 FROM refunds n FULL JOIN finance_old_refunds o USING(id) WHERE n.id IS NULL OR o.id IS NULL OR n.fee IS DISTINCT FROM o.fee*10)
 OR EXISTS(SELECT 1 FROM ledger n FULL JOIN finance_old_ledger o USING(id) WHERE n.id IS NULL OR o.id IS NULL OR (n.amount,n.balance_after) IS DISTINCT FROM (o.amount*10,o.balance_after*10))
 OR EXISTS(SELECT 1 FROM redeem_codes n FULL JOIN finance_old_codes o USING(id) WHERE n.id IS NULL OR o.id IS NULL OR n.amount IS DISTINCT FROM o.amount*10)
 OR EXISTS(SELECT 1 FROM redemptions n FULL JOIN finance_old_redemptions o USING(id) WHERE n.id IS NULL OR o.id IS NULL OR n.amount IS DISTINCT FROM o.amount*10) THEN
  RAISE EXCEPTION 'Row-level verification failed; rolling back all changes';
 END IF;
 SELECT jsonb_build_object(
  'users',(SELECT jsonb_build_object('rows',count(*),'balance',COALESCE(sum(balance),0),'frozen',COALESCE(sum(frozen),0),'service_fee',COALESCE(sum(service_fee),0)) FROM users),
  'refunds',(SELECT jsonb_build_object('rows',count(*),'fee',COALESCE(sum(fee),0)) FROM refunds),
  'ledger',(SELECT jsonb_build_object('rows',count(*),'amount',COALESCE(sum(amount),0),'balance_after',COALESCE(sum(balance_after),0)) FROM ledger),
  'redeem_codes',(SELECT jsonb_build_object('rows',count(*),'amount',COALESCE(sum(amount),0)) FROM redeem_codes),
  'redemptions',(SELECT jsonb_build_object('rows',count(*),'amount',COALESCE(sum(amount),0)) FROM redemptions)
 ) INTO after_totals;
 INSERT INTO finance_data_migrations(id,factor,summary) VALUES('history-x10-20261105',10,jsonb_build_object('before',before_totals,'after',after_totals));
 INSERT INTO audit_logs(actor_id,action,target_id,metadata) VALUES(actor,'finance.history_rescale','history-x10-20261105',
  jsonb_build_object('factor',10,'before',before_totals,'after',after_totals,'note','Historical financial amounts multiplied once. Earlier audit metadata retains original values. Future financial operations and API units unchanged.'));
END
$migration$;
COMMIT;

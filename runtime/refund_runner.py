"""Isolated protocol process. Credentials arrive through stdin, never argv/files.

Uses the Space password/TOTP login and merge2 request implementation unchanged.
Every mutation checkpoint waits for a durable Go/PostgreSQL acknowledgement.
"""
import json
import sys
import time
import uuid
from urllib.parse import quote, urlencode

from curl_cffi import requests
from manager_oauth import bridge, upstream
from manager_oauth.merge_v2 import TeamProtocolLogin


class StopProtocol(BaseException):
    pass


class NeedsVerification(Exception):
    pass


class UpstreamFailure(Exception):
    def __init__(self, code, status=0, network_code=0):
        self.code, self.status, self.network_code = code, status, network_code


def rpc(method, params=None, **kwargs):
    print(json.dumps({"type": "checkpoint", "method": method, **(params or {}), **kwargs}), flush=True)
    reply = sys.stdin.readline()
    if not reply or json.loads(reply).get("ok") is not True:
        raise StopProtocol()
    return {}


class CustomerLogin(TeamProtocolLogin):
    def log(self, step, message, level="info"):
        if step == "waiting_code":
            raise NeedsVerification()
        super().log(step, message, level)


class MotherClient:
    def __init__(self, job):
        self.mother = job["mother"]
        self.email = job["account"]["email"].casefold()
        if self.email == self.mother["email"].casefold():
            raise UpstreamFailure("mother_identity")
        claims = upstream.jwt_payload(self.mother["access_token"])
        team = (claims.get("https://api.openai.com/auth") or {}).get("chatgpt_account_id")
        email = (claims.get("https://api.openai.com/profile") or {}).get("email") or claims.get("email")
        if (team and team != self.mother["team_id"]) or (email and email.casefold() != self.mother["email"].casefold()):
            raise UpstreamFailure("mother_identity")
        self.base = "https://chatgpt.com/backend-api/accounts/" + quote(self.mother["team_id"], safe="")
        proxy = job.get("mother_proxy")
        if not proxy:
            raise UpstreamFailure("proxy_missing")
        self.session = requests.Session(impersonate="chrome", proxies=upstream._cffi_proxies(proxy))
        self.headers = {
            "Authorization": "Bearer " + self.mother["access_token"],
            "chatgpt-account-id": self.mother["team_id"],
            "Origin": "https://chatgpt.com", "Referer": "https://chatgpt.com/admin/members",
            "Accept": "application/json", "oai-language": "zh-CN",
        }

    def call(self, method, suffix, body=None, timeout=60):
        args = {"headers": self.headers, "timeout": timeout, "allow_redirects": False}
        if body is not None:
            args["json"] = body
        try:
            response = self.session.request(method, self.base + suffix, **args)
        except Exception as exc:
            raise UpstreamFailure("mother_network", network_code=getattr(exc,"code",0)) from None
        if not 200 <= response.status_code < 300:
            raise UpstreamFailure("mother_http", response.status_code)
        return response

    def find(self, collection):
        # Read only this email, with complete bounded pagination. An unknown shape
        # must never be interpreted as "already removed".
        offset = 0
        for _ in range(100):
            query = {"offset": offset, "limit": 100, "query": self.email}
            if collection == "invites":
                query.update(include_pending="true", include_requests="true")
            response = self.call("GET", "/" + collection + "?" + urlencode(query))
            try:
                data = response.json()
            except (ValueError, TypeError):
                raise UpstreamFailure("mother_shape") from None
            if not isinstance(data, dict):
                raise UpstreamFailure("mother_shape")
            items = next((data[k] for k in ("items", "users", "members", "invites", "data") if isinstance(data.get(k), list)), None)
            if items is None or any(not isinstance(x, dict) for x in items):
                raise UpstreamFailure("mother_shape")
            total = data.get("total")
            has_more = data.get("has_more")
            if (("total" in data and (type(total) is not int or total < offset + len(items)))
                    or ("has_more" in data and type(has_more) is not bool)
                    or (has_more is False and total is not None and offset + len(items) < total)):
                raise UpstreamFailure("mother_shape")
            for row in items:
                email = row.get("email") or row.get("email_address")
                if not isinstance(email, str) or not email:
                    raise UpstreamFailure("mother_shape")
                if email.casefold() == self.email:
                    return row
            offset += len(items)
            if has_more is True or (total is not None and offset < total):
                if not items:
                    raise UpstreamFailure("mother_shape")
                continue
            if has_more is False or total is not None or len(items) < 100:
                return None
        raise UpstreamFailure("mother_pagination")

    def invite(self):
        # Never clear all ordinary members. Only handle the requested customer.
        if self.find("users") or self.find("invites"):
            return
        self.call("POST", "/invites", {
            "email_addresses": [self.email], "flow_id": str(uuid.uuid4()),
            "resend_emails": True, "role": "standard-user",
            "seat_type": self.mother["seat_type"], "submission_id": str(uuid.uuid4()),
        })

    def preflight(self):
        # Always before the first invitation checkpoint. This GET cannot create
        # an invitation; a failure here is safe to return to the shared queue.
        try:
            self.find("users")
        except UpstreamFailure as exc:
            code = "mother_preflight_auth" if exc.status == 401 else "mother_preflight_failed"
            raise UpstreamFailure(code, exc.status, exc.network_code) from None

    def remove(self, child_id):
        row = self.find("users")
        if row is None:
            return  # Complete list confirms removal, including a lost DELETE response.
        ids = {str(row.get(k) or "") for k in ("id", "user_id", "chatgpt_user_id")}
        role = str(row.get("role") or "").lower()
        if not child_id or child_id not in ids or role not in {"standard-user", "member", "user"}:
            raise UpstreamFailure("member_identity")
        self.call("DELETE", "/users/" + quote(child_id, safe=""))

    def confirm_removed(self):
        if self.find("users") is not None or self.find("invites") is not None:
            raise UpstreamFailure("cleanup_pending")

    def cleanup_unaccepted(self, child_id):
        row = self.find("users")
        if row is not None:
            identity = child_id or str(row.get("user_id") or row.get("id") or "")
            self.remove(identity)
        # No verified cancellation contract is available for pending invites.
        # Preserve the reservation instead of guessing an endpoint or hiding it.
        if self.find("invites") is not None:
            raise UpstreamFailure("cleanup_invite_pending")
        self.confirm_removed()


def execute(job, checkpoint=rpc, mother_factory=MotherClient, login_factory=CustomerLogin, sleep=time.sleep):
    recovery = (job.get("settings", {}).get("recovery") or {}).get("enabled", False)
    mode = job.get("recovery_mode", "")
    step = job["step"]
    child_id = job.get("child_user_id") or ""
    accepted = step in {"accepted", "removing"}
    if step == "submitting" and mode != "verify":
        return {"ok": False, "code": "export_uncertain"}
    if not job.get("mother_proxy") or (not accepted and mode != "cleanup" and not job.get("child_proxy")):
        return {"ok": False, "code": "proxy_missing"}
    mother = None
    try:
        mother = mother_factory(job)
        if step == "assigned" and mode != "cleanup":
            mother.preflight()
        if mode == "cleanup" and not accepted:
            checkpoint("cleanup_started")
            mother.cleanup_unaccepted(child_id)
            checkpoint("cleanup_completed")
            return {"ok": True}
        if step in {"assigned", "inviting"}:
            checkpoint("inviting")
            mother.invite()
            checkpoint("invited")
        if not accepted:
            account = job["account"]
            payload = {
                "email": account["email"], "password": account["password"],
                "_totp_secret": account["totp_secret"], "proxy": job["child_proxy"],
                "credential_mode": "chatgpt_at", "configured_login_mode": "password_totp",
                "selected_login_mode": "password_totp", "allow_sms": False,
                "skip_phone_verification": True, "merge_v2": {"team_id": job["mother"]["team_id"]},
            }
            if mode == "verify":
                from refund_recovery import verify
                bridge.configure(payload, lambda *_: (_ for _ in ()).throw(StopProtocol()))
                return verify(job, checkpoint, login_factory, payload)

            def flow_rpc(method, params):
                nonlocal child_id, accepted
                if method == "merge_v2_ready":
                    child_id = params["user_id"]
                checkpoint(method, params)
                if method == "merge_v2_accepted":
                    accepted = True
                return {}

            attempts = 1 if recovery else job["settings"]["max_attempts"]
            for attempt in range(attempts):
                checkpoint("login")
                bridge.configure(payload, flow_rpc)
                flow = None
                try:
                    flow = login_factory(job["id"], payload)
                    session = flow.login()
                    if recovery and not job.get("personal_baseline"):
                        from refund_recovery import personal_snapshot, VerificationUnavailable
                        try:
                            baseline = personal_snapshot(flow, session)
                        except VerificationUnavailable:
                            baseline = {"id": "", "state": "unknown"}
                        checkpoint("personal_baseline", {"baseline": baseline})
                    result = flow.submit_export(session)
                except NeedsVerification:
                    return {"ok": False, "code": "extra_verification"}
                except bridge.DeadAccountError:
                    return {"ok": False, "code": "account_unavailable"}
                except Exception as exc:
                    retry = (isinstance(exc, upstream.LoginFlowError) and exc.retryable) or upstream._is_transient_login_error(str(exc))
                    result = {"success": False, "retryable": retry, "error_code": "login_failed"}
                finally:
                    if flow is not None:
                        flow.close()
                if result.get("success") and accepted:
                    break
                if accepted or not result.get("retryable") or attempt + 1 == attempts:
                    return {"ok": False, "code": result.get("error_code") or "login_failed", "http_status": result.get("http_status", 0),
                            **({"retryable": bool(result.get("retryable")), "network_code": result.get("network_code", 0), "duration_ms": result.get("duration_ms", 0)} if recovery else {})}
                sleep(job["settings"]["retry_seconds"])
        if not accepted:
            return {"ok": False, "code": "export_uncertain"}
        checkpoint("removing")
        mother.remove(child_id)
        if recovery:
            mother.confirm_removed()
        checkpoint("completed")
        return {"ok": True}
    except StopProtocol:
        return {"ok": False, "code": "checkpoint_declined"}
    except UpstreamFailure as exc:
        return {"ok": False, "code": exc.code, "http_status": exc.status,
                **({"network_code": exc.network_code, "retryable": exc.code in {"mother_network", "cleanup_pending"} or exc.status in {408, 429, 500, 502, 503, 504}} if recovery else {})}
    except Exception:
        return {"ok": False, "code": "protocol_failed"}
    finally:
        if mother is not None:
            mother.session.close()


def probe_mother(job):
    from resource_probe import check_mother
    return check_mother(job, MotherClient, UpstreamFailure)


if __name__ == "__main__":
    if "--check" in sys.argv:
        import pyotp
        import shutil
        if not shutil.which("node"):
            raise SystemExit("Node.js is required")
        print("protocol runtime ready")
    elif "--probe-mother" in sys.argv or "--probe-proxy" in sys.argv:
        try:
            if "--probe-proxy" in sys.argv:
                from resource_probe import check_proxy
                result = check_proxy(json.load(sys.stdin))
            else:
                result = probe_mother(json.load(sys.stdin))
        except BaseException:
            result = {"valid": False, "http_status": 0, "latency_ms": 0, "code":"runtime"}
        print(json.dumps(result), flush=True)
    else:
        try:
            job = json.loads(sys.stdin.readline())
            result = execute(job)
        except BaseException:
            result = {"ok": False, "code": "protocol_failed"}
        print(json.dumps({"type": "result", **result}), flush=True)

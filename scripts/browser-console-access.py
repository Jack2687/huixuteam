"""Console placement/authorization QA against a fresh database on port 18743."""
import json
import os
import secrets
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CONFIG = json.loads((ROOT / '.local/console-qa-config.json').read_text(encoding='utf-8'))
assert '/huixu_console_qa_' in CONFIG['DATABASE_URL'] and CONFIG['PORT'] == '18743'
BASE = 'http://127.0.0.1:18743'
BROWSER = Path(os.environ['APPDATA']) / 'npm/node_modules/agent-browser/bin/agent-browser-win32-x64.exe'
SESSION = 'huixu-console-access-qa'
SHOTS = ROOT / '.local/console-access-qa'
SHOTS.mkdir(exist_ok=True)


def batch(*commands):
    p = subprocess.run([str(BROWSER), '--session', SESSION, 'batch', '--json'], input=json.dumps(commands, ensure_ascii=False), capture_output=True, encoding='utf-8', timeout=55)
    if p.returncode:
        raise RuntimeError('Browser command failed: ' + p.stderr[:200])
    results = json.loads(p.stdout)
    for item in results:
        if not item['success']:
            raise RuntimeError(str(item.get('error'))[:300])
    return [item['result'] for item in results]


def ref(label, role='button'):
    state = batch(['snapshot', '-i'])[0]
    matches = ['@' + key for key, value in state['refs'].items() if value['name'] == label and value['role'] == role]
    assert len(matches) == 1, 'Expected one element: ' + label
    return matches[0]


def click(label):
    batch(['click', ref(label)])


def js(script):
    return batch(['eval', script])[0]['result']


def api(path, method='GET', value=None):
    options = {'method': method, 'headers': {'Content-Type': 'application/json'}}
    if value is not None:
        options['body'] = json.dumps(value)
    return js('fetch(' + json.dumps(path) + ',' + json.dumps(options) + ').then(async r=>({status:r.status,data:await r.json()}))')


def login(email, password):
    batch(['fill', ref('账户邮箱', 'textbox'), email], ['fill', ref('账户密码', 'textbox'), password])
    click('进入服务台')
    batch(['wait', '.app-shell'])


def shot(name):
    batch(['screenshot', str(SHOTS / (name + '.png'))])


def assert_member_surface():
    assert not js('document.body.textContent.includes("运营控制台")'), 'Console label leaked'
    assert js('document.querySelectorAll(".topbar-console").length') == 0
    assert not js('performance.getEntriesByType("resource").some(v=>v.name.includes("/assets/admin-"))'), 'Non-admin loaded a console bundle'
    nav = api('/api/workspace/navigation')
    assert nav['status'] == 200 and all(v['key'] != 'admin' for v in nav['data']['menus'])
    assert not any(v.startswith('admin.') for v in api('/api/auth/me')['data']['permissions'])


try:
    subprocess.run([str(BROWSER), '--session', SESSION, 'open', BASE], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=45, check=True)
    batch(['set', 'viewport', '1440', '1000'], ['wait', '.account-access input[type="email"]'])
    login(CONFIG['BOOTSTRAP_EMAIL'], CONFIG['BOOTSTRAP_PASSWORD'])
    batch(['wait', '.topbar-console'])
    assert js('document.querySelectorAll(".topbar .topbar-console").length') == 1
    assert not js('document.querySelector(".sidebar").textContent.includes("运营控制台")')
    assert js('document.querySelector(".topbar-console").getBoundingClientRect().left>innerWidth/2')
    shot('administrator-desktop')
    click('运营控制台')
    batch(['wait', '.admin-panel'])
    assert js('document.querySelector(".topbar-console").getAttribute("aria-current")') == 'page'
    shot('administrator-console')
    for width in (390, 320):
        batch(['set', 'viewport', str(width), '844'])
        assert not js('document.documentElement.scrollWidth>innerWidth'), 'Mobile page overflow'
        assert js('(()=>{const r=document.querySelector(".topbar-console").getBoundingClientRect();return r.left>=0&&r.right<=innerWidth&&r.top<70})()')
        shot('administrator-mobile-' + str(width))
    batch(['set', 'viewport', '1440', '1000'])
    users = []
    for role in ('member', 'operator'):
        email = 'console-' + role + '-' + secrets.token_hex(4) + '@test.invalid'
        password = secrets.token_urlsafe(24)
        assert api('/api/admin/users', 'POST', {'email': email, 'name': '权限验收' + role, 'password': password, 'role': role, 'service_fee': 3000})['status'] == 200
        users.append((role, email, password))
    assets = ['/assets/' + p.name for p in (ROOT / 'webui/static/assets').glob('admin-*')]
    assert assets
    for role, email, password in users:
        click('退出账户')
        batch(['wait', '.account-access input[type="email"]'])
        js('performance.clearResourceTimings()')
        login(email, password)
        assert_member_surface()
        # A fresh page denies hash-based entry without loading the lazy console.
        batch(['open', BASE + '/#admin'], ['wait', '.app-shell'])
        assert_member_surface()
        assert js('location.hash') != '#admin'
        shot(role + '-desktop')
        for path in ('/workspace/admin', '/workspace/admin/', '/workspace/admin/settings', '/api/admin/overview', '/api/admin/refunds', '/api/admin/site-settings', *assets):
            assert js('fetch(' + json.dumps(path) + ').then(r=>r.status)') == 403, 'Non-admin access: ' + path
        batch(['set', 'viewport', '390', '844'])
        assert not js('document.documentElement.scrollWidth>innerWidth')
        shot(role + '-mobile')
        batch(['set', 'viewport', '1440', '1000'])
    (SHOTS / 'result.json').write_text(json.dumps({'passed': True, 'administrator_top_right_only': True, 'member_operator_backend_denied': True, 'logout_role_switch_checked': True, 'mobile_widths': [320, 390], 'admin_assets_checked': len(assets)}), encoding='utf-8')
    print('PASS: top-right admin entry, no sidebar duplicate, mobile layouts, role switching, non-admin navigation/hash/API/page/bundle denial', flush=True)
except Exception:
    try:
        shot('failure')
    except Exception:
        pass
    raise
finally:
    try:
        batch(['close'])
    except Exception:
        pass

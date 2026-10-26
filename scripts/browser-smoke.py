"""Browser QA against the isolated llm_refund_qa service (port 18743 only).

Requires agent-browser on PATH, a running QA server, and the local .env.
Credentials and generated codes stay in memory; only non-sensitive assertions print.
"""
import json
import base64
import os
import subprocess
import shutil
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
sys.stdout.reconfigure(encoding='utf-8')
sys.stderr.reconfigure(encoding='utf-8')
CONFIG = dict(line.split('=', 1) for line in (ROOT / '.env').read_text(encoding='utf-8-sig').splitlines() if '=' in line)
SESSION = 'refund-ui-qa'
BASE = 'http://127.0.0.1:18743'
ARTIFACTS = ROOT / '.local' / 'browser-qa'
ARTIFACTS.mkdir(parents=True, exist_ok=True)
BROWSER = Path(os.environ.get('APPDATA', '')) / 'npm/node_modules/agent-browser/bin/agent-browser-win32-x64.exe'
COMMAND = [str(BROWSER)] if BROWSER.exists() else [shutil.which('agent-browser') or 'agent-browser']

def batch(*commands):
    process = subprocess.run(COMMAND + ['--session', SESSION, 'batch', '--json'], input=json.dumps(commands, ensure_ascii=False), capture_output=True, encoding='utf-8', timeout=45)
    if process.returncode:
        raise RuntimeError('Browser command failed: ' + process.stderr[-400:])
    result = json.loads(process.stdout)
    for item in result:
        if not item['success']:
            raise RuntimeError(str(item.get('error')))
    return [item['result'] for item in result]

def snapshot():
    return batch(['snapshot', '-i'])[0]

def ref(name, role=None):
    state = snapshot()
    found = []
    for key, value in state['refs'].items():
        if value['name'] == name and (not role or value['role'] == role):
            found.append('@' + key)
    if found:
        return sorted(found, key=lambda key: int(key[2:]))[-1]
    raise AssertionError(f'Missing UI element: {name} ({role})')

def click(name, role='button'):
    target = ref(name, role)
    batch(['scrollintoview', target], ['click', target], ['wait', '180'])

def fill(name, value, role='textbox'):
    batch(['fill', ref(name, role), value])

def text():
    result = batch(['get', 'text', 'body'])[0]
    return result.get('text', '')

def expect(needle):
    for _ in range(15):
        if needle in text():
            print('Verified: ' + needle, flush=True)
            return
        time.sleep(.15)
    shot('failure')
    print(batch(['errors'], ['console']), flush=True)
    raise AssertionError('Missing visible text: ' + needle)

def evaluate(script):
    return batch(['eval', script])[0]['result']

def shot(name):
    batch(['screenshot', str(ARTIFACTS / (name + '.png'))])

try:
    # Start the daemon without captured pipes on Windows; a detached child can
    # otherwise retain the parent's stdout pipe and prevent communicate() ending.
    subprocess.run(COMMAND + ['--session', SESSION, 'open', BASE], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=45, check=True)
    batch(['open', BASE], ['set', 'viewport', '1440', '1000'])
    fill('登录邮箱', CONFIG['BOOTSTRAP_EMAIL']); fill('账户密码', CONFIG['BOOTSTRAP_PASSWORD']); click('进入服务台')
    expect('Admin，欢迎回来。'); shot('overview')
    click('运营控制台'); click('服务资源与调度', 'tab'); expect('单个母号依次办理')
    suffix = str(int(time.time()))
    proxy_name = '验收代理-' + suffix
    click('录入代理'); fill('代理名称', proxy_name); fill('代理地址', 'proxy.invalid:8080:fixture:private-secret')
    click('保存'); expect(proxy_name)
    child_proxy_name = '子号出口-' + suffix
    click('录入代理'); fill('代理名称', child_proxy_name); fill('代理地址', 'child.invalid:8081:fixture-child:private-child-exit'); click('保存'); expect(child_proxy_name)
    config = evaluate('fetch("/api/admin/refund-settings").then(r=>r.json())')
    child_proxy_id = next(p['id'] for p in config['proxies'] if p['name'] == child_proxy_name)
    batch(['select', ref('子号全局代理', 'combobox'), child_proxy_id]); click('应用调度配置'); expect('执行设置已保存')
    mother_name, mother_email, mother_team = '验收母号-' + suffix, 'mother-' + suffix + '@example.test', 'qa-team-' + suffix
    claims = {'https://api.openai.com/profile': {'email': mother_email}, 'https://api.openai.com/auth': {'chatgpt_account_id': mother_team, 'chatgpt_plan_type': 'team'}, 'exp': 2000000000}
    fixture_at = 'eyJhbGciOiJub25lIn0.' + base64.urlsafe_b64encode(json.dumps(claims).encode()).decode().rstrip('=') + '.fixture'
    session_json = json.dumps({'accessToken': fixture_at, 'refresh_token': 'fixture-private-rt', 'user': {'email': mother_email}, 'account': {'id': mother_team, 'planType': 'team'}})
    click('录入母号'); fill('母号名称', mother_name); fill('Session JSON / Access Token', session_json); click('解析凭据'); expect('已读取 RT')
    assert evaluate('document.querySelector("input[type=email]").value') == mother_email
    assert evaluate('Array.from(document.querySelectorAll("label")).find(l=>l.textContent.trim()==="团队 ID").querySelector("input").value') == mother_team
    options = evaluate('Array.from(document.querySelectorAll("select option")).filter(o=>o.textContent.includes(' + json.dumps(proxy_name) + ')).map(o=>o.value)')
    batch(['select', ref('母号专属代理', 'combobox'), options[0]], ['uncheck', ref('启用母号', 'checkbox')]); click('保存'); expect(mother_name)
    config = evaluate('fetch("/api/admin/refund-settings").then(r=>r.json())')
    assert config['settings']['enabled'] is False
    assert config['settings']['child_proxy_id'] == child_proxy_id
    saved_mother = next(m for m in config['mothers'] if m['name'] == mother_name)
    assert saved_mother['proxy_id'] != child_proxy_id and saved_mother['has_refresh'] and saved_mother['email'] == mother_email and saved_mother['team_id'] == mother_team
    assert all(secret not in json.dumps(config) for secret in [fixture_at, 'fixture-private-rt', 'private-secret', 'private-child-exit'])
    evaluate('(()=>{const row=[...document.querySelectorAll("tr")].find(r=>r.textContent.includes('+json.dumps(mother_name)+'));[...row.querySelectorAll("button")].find(b=>b.textContent.trim()==="编辑").click()})()')
    assert evaluate('document.querySelector("textarea").value') == ''
    assert evaluate('document.querySelector("input[autocomplete=new-password]").value') == ''
    click('取消')
    shot('refund-execution')
    batch(['set', 'viewport', '390', '844']); shot('refund-execution-mobile')
    assert not evaluate('document.documentElement.scrollWidth > innerWidth')
    batch(['set', 'viewport', '1440', '1000'])
    if '--resources-only' in sys.argv:
        print('Verified: proxy shorthand, separate global/dedicated proxies, Session import, blank credential edit, desktop/mobile privacy', flush=True)
        sys.exit(0)
    click('账户管理', 'tab'); click('新增账户')
    email = f'ui-{int(time.time())}@example.test'
    password = 'Local-UI-Test-Password-2026'
    fill('账户称呼', '验收用户'); fill('邮箱', email); fill('首次登录密码', password); click('保存账户')
    expect(email)
    click('兑换码', 'tab'); click('生成兑换码'); fill('积分面额', '100', 'spinbutton'); click('生成兑换码')
    expect('已生成 1 个兑换码')
    code = evaluate('document.querySelector(".generated-codes").textContent.trim()')
    assert code.startswith('HUIXU-')
    click('已安全保存'); expect('可兑换'); shot('admin-codes')
    click('权限说明', 'tab'); expect('提供三类角色'); shot('permissions')
    click('公告发布', 'tab'); click('撰写公告')
    bulletin = '回序公告验收-' + suffix
    fill('公告标题', bulletin); fill('公告正文', '这是仅用于本地验收的公告。\n<script>window.announcementXSS=true</script>')
    click('保存草稿'); expect('草稿已保存')
    feed = evaluate('fetch("/api/announcements").then(r=>r.json())')
    assert not any(x['title'] == bulletin for x in feed['items'])
    def row_action(action):
        evaluate('(()=>{const row=[...document.querySelectorAll("tbody tr")].find(r=>r.textContent.includes('+json.dumps(bulletin)+'));const button=[...row.querySelectorAll("button")].find(b=>b.textContent.trim()==='+json.dumps(action)+');if(!button)throw Error("Announcement action missing");button.click()})()')
        batch(['wait','250'])
    row_action('发布'); expect('公告已发布')
    row_action('置顶'); expect('已置顶公告'); shot('announcements-admin')
    click('公告中心'); expect(bulletin); shot('announcements-user')
    assert evaluate('window.announcementXSS === undefined')
    click('运营控制台'); click('公告发布','tab'); row_action('撤下'); expect('公告已撤下')
    feed = evaluate('fetch("/api/announcements").then(r=>r.json())')
    assert not any(x['title'] == bulletin for x in feed['items'])
    row_action('发布'); expect('公告已发布')
    click('退出账户'); fill('登录邮箱', email); fill('账户密码', password); click('进入服务台'); expect('验收用户，欢迎回来。')
    assert '运营控制台' not in snapshot()['snapshot']
    click('公告中心'); expect(bulletin)
    assert evaluate('fetch("/api/admin/announcements").then(r=>r.status)') == 403

    denied = evaluate('fetch("/api/admin/users").then(r=>r.status)'); assert denied == 403
    click('资金与账单'); fill('兑换码', code); click('兑换到本账户'); expect('兑换成功，100 分'); expect('兑换入账'); shot('billing')
    fill('兑换码', code); click('兑换到本账户'); expect('已使用过此兑换码')
    click('申请记录'); click('提交申请'); fill('服务账号邮箱', 'refund-ui@example.test'); fill('服务账号密码', 'Dummy-Account-Password'); fill('验证器密钥（2FA）', 'JBSWY3DPEHPK3PXP')
    batch(['check', ref('我有权使用以上账号，并授权服务方使用所填资料办理本次退款服务。', 'checkbox')])
    click('确认提交 1 笔'); expect('排队等待自动处理'); expect('refund-ui@example.test'); shot('refunds')
    click('查看 refund-ui@example.test 的申请'); expect('服务单详情'); expect('已冻结'); shot('detail')
    click('撤回申请'); click('确认撤回'); expect('已释放'); click('关闭')
    click('资金与账单'); expect('预留解除'); expect('100 分')
    click('接口接入'); click('生成密钥'); fill('用途名称', 'UI 验收密钥'); click('确认生成')
    expect('请现在保存密钥')
    api_key = evaluate('document.querySelector(".secret-box code").textContent')
    assert api_key.startswith('hx_')
    click('已安全保存'); expect('UI 验收密钥'); shot('developer')
    status = evaluate('fetch("/open/v1/requests",{headers:{Authorization:"Bearer "+' + json.dumps(api_key) + '}}).then(r=>r.status)'); assert status == 200
    click('停用'); click('确认停用'); expect('已停用')
    status = evaluate('fetch("/open/v1/requests",{headers:{Authorization:"Bearer "+' + json.dumps(api_key) + '}}).then(r=>r.status)'); assert status == 401
    click('使用指南'); batch(['click', '.help-card details:first-child summary']); expect('每 1 积分对应 1 R')
    batch(['set', 'viewport', '390', '844'], ['open', BASE + '/#overview'], ['wait', '250']); shot('mobile')
    overflow = evaluate('document.documentElement.scrollWidth > innerWidth'); assert not overflow
    click('展开导航'); expect('服务账户'); shot('mobile-menu'); click('资金与账单'); expect('兑换入账'); shot('mobile-billing')
    overflow = evaluate('document.documentElement.scrollWidth > innerWidth'); assert not overflow
    batch(['set','viewport','1440','1000'],['open',BASE+'/developers/reference'])
    expect('回序开放接口'); shot('api-reference')
    assert '/v1/refunds' not in text() and '/open/v1/requests' in text()
    batch(['set','viewport','390','844']); shot('api-reference-mobile')
    assert not evaluate('document.documentElement.scrollWidth > innerWidth')
    print('PASS: announcements draft/publish/pin/withdraw, escaped content, API documentation, login, admin user creation, role isolation, code generation and redemption, duplicate prevention, refund creation/cancellation, billing, API keys, help, desktop/mobile layout')
finally:
    try: batch(['close'])
    except Exception: pass

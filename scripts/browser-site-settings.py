"""Site settings browser QA: dedicated 18743 database + loopback SMTP only.
Run after preparing .local/site-qa-config.json and its isolated QA server.
Never sends mail to an external service or starts the migrated local database worker.
"""
import email
import json
import os
import queue
import re
import secrets
import socketserver
import subprocess
import threading
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CONFIG = json.loads((ROOT / '.local/site-qa-config.json').read_text(encoding='utf-8'))
assert '/huixu_site_qa_' in CONFIG['DATABASE_URL'] and CONFIG['PORT'] == '18743'
BASE = 'http://127.0.0.1:18743'
BROWSER = Path(os.environ['APPDATA']) / 'npm/node_modules/agent-browser/bin/agent-browser-win32-x64.exe'
SESSION = 'huixu-site-admin-qa'
SHOTS = ROOT / '.local/site-settings-qa'
SHOTS.mkdir(exist_ok=True)
messages = queue.Queue()

class SMTP(socketserver.StreamRequestHandler):
    def handle(self):
        self.request.settimeout(15)
        self.wfile.write(b'220 localhost ESMTP\r\n')
        while True:
            line = self.rfile.readline().strip()
            if not line:
                return
            if line.startswith((b'EHLO', b'HELO', b'MAIL FROM:', b'RCPT TO:')):
                self.wfile.write(b'250 localhost\r\n')
            elif line == b'DATA':
                self.wfile.write(b'354 End with dot\r\n')
                parts = []
                while True:
                    part = self.rfile.readline()
                    if part in (b'.\r\n', b''):
                        break
                    parts.append(part)
                msg = email.message_from_bytes(b''.join(parts))
                messages.put(msg.get_payload(decode=True).decode('utf-8'))
                self.wfile.write(b'250 accepted\r\n')
            elif line == b'QUIT':
                self.wfile.write(b'221 bye\r\n')
                return
            else:
                self.wfile.write(b'500 Unsupported\r\n')

def batch(*commands):
    p = subprocess.run([str(BROWSER), '--session', SESSION, 'batch', '--json'], input=json.dumps(commands, ensure_ascii=False), capture_output=True, encoding='utf-8', timeout=55)
    if p.returncode:
        raise RuntimeError('Browser command failed')
    result = json.loads(p.stdout)
    for item in result:
        if not item['success']:
            raise RuntimeError(str(item.get('error'))[:300])
    return [item['result'] for item in result]

def ref(label, role=None):
    state = batch(['snapshot', '-i'])[0]
    exact = []; partial = []
    for key, value in state['refs'].items():
        if role and value['role'] != role:
            continue
        if value['name'] == label:
            exact.append('@' + key)
        elif value['name'].startswith(label):
            partial.append('@' + key)
    found = exact or partial
    if not found:
        raise AssertionError('Missing element: ' + label)
    return found[-1]

def click(label, role='button'):
    target = ref(label, role)
    batch(['scrollintoview', target], ['click', target], ['wait', '250'])

def fill(label, value, role=None):
    batch(['fill', ref(label, role), value])

def text():
    return batch(['get', 'text', 'body'])[0]['text']

def expect(value):
    for _ in range(30):
        if value in text():
            return
        time.sleep(.15)
    raise AssertionError('Missing visible text: ' + value)

def js(script):
    return batch(['eval', script])[0]['result']

def code():
    value = messages.get(timeout=12)
    found = re.search(r'\b[0-9]{6}\b', value)
    assert found, 'No verification code in local sink'
    return found.group()

def shot(name):
    batch(['screenshot', str(SHOTS / (name + '.png'))])

def open_browser():
    subprocess.run([str(BROWSER), '--session', SESSION, 'open', BASE], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=45, check=True)
    batch(['open', BASE], ['set', 'viewport', '1440', '1000'], ['wait', '300'])

server = socketserver.ThreadingTCPServer(('127.0.0.1', 0), SMTP)
server.daemon_threads = True
threading.Thread(target=server.serve_forever, daemon=True).start()
try:
    open_browser()
    fill('账户邮箱', CONFIG['BOOTSTRAP_EMAIL'])
    fill('账户密码', CONFIG['BOOTSTRAP_PASSWORD'])
    click('进入服务台')
    expect('站点验收管理员')
    click('运营控制台'); click('站点设置', 'tab'); expect('前台功能')
    shot('feature-switches')
    batch(['uncheck', ref('接口接入', 'switch')])
    click('保存设置'); expect('站点设置已保存')
    assert js('fetch("/api/keys").then(r=>r.status)') == 404
    assert js('fetch("/api/workspace/navigation").then(r=>r.json()).then(v=>v.menus.some(x=>x.key==="developer"))') is False
    click('邮件设置')
    fill('SMTP 主机', '127.0.0.1'); fill('端口', str(server.server_address[1]))
    batch(['select', ref('加密方式', 'combobox'), 'none'])
    fill('发件人邮箱', 'mail@test.invalid'); fill('发件人名称', '回序邮件验收')
    click('保存设置'); expect('站点设置已保存')
    click('测试连接'); expect('SMTP 连接与认证成功')
    fill('测试收件邮箱', 'sink@test.invalid'); click('发送测试邮件')
    assert '测试邮件' in messages.get(timeout=12)
    click('刷新记录'); expect('已发送'); shot('mail-settings')
    click('安全与认证')
    batch(['check', ref('开放注册', 'switch')])
    fill('邮箱域名白名单', '@test.invalid\n*.example.com')
    click('保存设置'); expect('站点设置已保存'); shot('security-settings')
    batch(['set', 'viewport', '390', '844']); shot('site-mobile')
    assert not js('document.documentElement.scrollWidth>innerWidth'), 'Mobile settings overflow'
    batch(['set', 'viewport', '1440', '1000'])
    click('退出账户')
    batch(['close'])
    SESSION = 'huixu-site-member-qa'
    open_browser()
    assert '运营控制台' not in text()
    click('注册新账户')
    address = 'browser-' + secrets.token_hex(4) + '@test.invalid'
    password = secrets.token_urlsafe(20)
    fill('显示名称', '邮箱注册验收用户'); fill('账户邮箱', address)
    click('获取验证码'); registration_code = code()
    fill('邮箱验证码', registration_code); fill('设置密码', password); fill('确认密码', password)
    shot('registration')
    click('创建账户'); expect('注册成功')
    fill('账户密码', password); click('进入服务台'); expect('邮箱注册验收用户')
    assert '运营控制台' not in text() and '接口接入' not in text()
    assert js('performance.getEntriesByType("resource").some(x=>x.name.includes("/assets/admin-"))') is False
    assert js('fetch("/api/admin/site-settings").then(r=>r.status)') == 403
    assert js('fetch("/workspace/admin").then(r=>r.status)') == 403
    asset = next((ROOT / 'webui/static/assets').glob('admin-site-*.js')).name
    assert js('fetch(' + json.dumps('/assets/' + asset) + ').then(r=>r.status)') == 403
    click('退出账户'); click('忘记密码？')
    fill('账户邮箱', address); click('获取验证码'); reset_code = code()
    new_password = secrets.token_urlsafe(20)
    fill('邮箱验证码', reset_code); fill('设置密码', new_password); fill('确认密码', new_password)
    click('确认重置密码'); expect('密码已重置')
    fill('账户密码', new_password); click('进入服务台'); expect('邮箱注册验收用户')
    batch(['set', 'viewport', '390', '844']); shot('member-mobile')
    assert not js('document.documentElement.scrollWidth>innerWidth'), 'Mobile account overflow'
    print('PASS: settings, SMTP connection/test delivery, registration, reset, menu/API/module permissions, mobile layout', flush=True)
    (SHOTS / 'result.json').write_text(json.dumps({'passed':True,'real_mail_sent':False,'ordinary_user_admin_modules_loaded':False}), encoding='utf-8')
except Exception:
    shot('failure')
    raise
finally:
    server.shutdown(); server.server_close()
    try:
        batch(['close'])
    except Exception:
        pass

"""Custom-menu UI QA; isolated database/18743 and a loopback-only external page."""
import json
import os
import secrets
import subprocess
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CONFIG = json.loads((ROOT / '.local/custom-menus-qa-config.json').read_text(encoding='utf-8'))
assert '/huixu_menus_qa_' in CONFIG['DATABASE_URL'] and CONFIG['PORT'] == '18743'
BASE = 'http://127.0.0.1:18743'
BROWSER = Path(os.environ['APPDATA']) / 'npm/node_modules/agent-browser/bin/agent-browser-win32-x64.exe'
SESSION = 'huixu-menus-admin-qa'
SHOTS = ROOT / '.local/custom-menus-qa'
SHOTS.mkdir(exist_ok=True)
received = []

class ExternalPage(BaseHTTPRequestHandler):
    def do_GET(self):
        received.append({'path': self.path, 'cookie': bool(self.headers.get('Cookie')), 'authorization': bool(self.headers.get('Authorization')), 'workspace_referer': (self.headers.get('Referer') or '').startswith(BASE)})
        body = '<!doctype html><meta charset="UTF-8"><title>本机菜单验收页面</title><style>body{font:16px system-ui;padding:36px;background:#f4f7ef;color:#304331}h1{font-size:28px}</style><h1>自定义页面已打开</h1><p>此页面仅用于本机测试，不连接真实充值服务。</p>'.encode()
        self.send_response(200); self.send_header('Content-Type', 'text/html; charset=utf-8'); self.end_headers(); self.wfile.write(body)

    def log_message(self, *args):
        pass

def batch(*commands):
    p = subprocess.run([str(BROWSER), '--session', SESSION, 'batch', '--json'], input=json.dumps(commands, ensure_ascii=False), capture_output=True, encoding='utf-8', timeout=55)
    if p.returncode:
        raise RuntimeError('Browser command failed: ' + p.stderr[:200])
    result = json.loads(p.stdout)
    for item in result:
        if not item['success']:
            raise RuntimeError(str(item.get('error'))[:300])
    return [item['result'] for item in result]

def ref(label, role=None, index=0):
    state = batch(['snapshot', '-i'])[0]
    found = ['@' + key for key, value in state['refs'].items() if value['name'] == label and (not role or value['role'] == role)]
    if len(found) <= index:
        raise AssertionError('Missing element: ' + label)
    return found[index]

def click(label, role='button', index=0):
    target = ref(label, role, index)
    batch(['scrollintoview', target], ['click', target], ['wait', '250'])

def fill(label, value, index=0):
    batch(['fill', ref(label, None, index), value])

def js(script):
    return batch(['eval', script])[0]['result']

def expect(text):
    for _ in range(25):
        if text in batch(['get', 'text', 'body'])[0]['text']:
            return
        time.sleep(.15)
    raise AssertionError('Missing text: ' + text)

def shot(name):
    batch(['screenshot', str(SHOTS / (name + '.png'))])

def open_browser():
    subprocess.run([str(BROWSER), '--session', SESSION, 'open', BASE], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=45, check=True)
    batch(['open', BASE], ['set', 'viewport', '1440', '1100'], ['wait', '300'])

def api(path, method='GET', value=None):
    options={'method': method, 'headers': {'Content-Type': 'application/json'}}
    if value is not None:
        options['body'] = json.dumps(value)
    return js('fetch('+json.dumps(path)+','+json.dumps(options)+').then(async r=>({status:r.status,data:await r.json()}))')

server = ThreadingHTTPServer(('127.0.0.1', 0), ExternalPage)
threading.Thread(target=server.serve_forever, daemon=True).start()
external = 'http://localhost:' + str(server.server_port)
try:
    open_browser()
    fill('账户邮箱', CONFIG['BOOTSTRAP_EMAIL']); fill('账户密码', CONFIG['BOOTSTRAP_PASSWORD']); click('进入服务台')
    expect('菜单验收管理员')
    assert api('/api/admin/site-settings/custom-menus','PUT',[])['status']==200
    click('运营控制台'); click('站点设置', 'tab'); click('自定义菜单')
    expect('添加你的第一个菜单')
    click('添加菜单'); fill('菜单名称', '充值中心'); fill('页面 URL', external + '/topup')
    batch(['check', ref('点击菜单时新窗口打开', 'checkbox')])
    icon = SHOTS / 'sample.svg'
    icon.write_text('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><rect x="2" y="4" width="20" height="16" rx="3" fill="#b34a25"/><path d="M4 9h16" stroke="white" stroke-width="2"/></svg>', encoding='utf-8')
    batch(['upload', 'input[aria-label="上传菜单 1 SVG"]', str(icon)])
    click('添加菜单'); fill('菜单名称', '服务帮助', 1); fill('页面 URL', external + '/help', 1)
    batch(['check', ref('隐藏内嵌页的“新窗口打开”按钮', 'checkbox', 1)])
    click('添加菜单'); fill('菜单名称', '内部管理资料', 2); fill('页面 URL', external + '/private', 2)
    batch(['select', ref('可见角色', 'combobox', 2), 'admin'])
    click('上移菜单 3'); click('保存自定义菜单'); expect('自定义菜单已保存')
    settings = api('/api/admin/site-settings')['data']['custom_menus']
    assert [v['label'] for v in settings] == ['充值中心', '内部管理资料', '服务帮助']
    assert settings[0]['open_in_new_tab'] and settings[0]['icon_svg']
    private = settings[1]['id']; help_id = settings[2]['id']; topup_id = settings[0]['id']
    batch(['scroll', 'up', '2000']); shot('custom-menus-desktop')
    batch(['set', 'viewport', '390', '844']); shot('custom-menus-mobile')
    assert not js('document.documentElement.scrollWidth>innerWidth'), 'Mobile editor overflow'
    batch(['set', 'viewport', '1440', '1000'])
    address='menu-member-'+secrets.token_hex(4)+'@test.invalid'; password=secrets.token_urlsafe(24)
    assert api('/api/admin/users','POST',{'email':address,'name':'菜单验收用户','password':password,'role':'member','service_fee':3000})['status']==200

    SESSION='huixu-menus-member-qa'; open_browser()
    fill('账户邮箱',address);fill('账户密码',password);click('进入服务台');expect('菜单验收用户')
    body=batch(['get','text','body'])[0]['text'];assert '运营控制台' not in body and '内部管理资料' not in body
    assert js('performance.getEntriesByType("resource").some(v=>v.name.includes("/assets/admin-"))') is False
    assert api('/api/workspace/custom-menus/'+private)['status']==403
    assert js('fetch('+json.dumps('/workspace/custom/'+private+'/open')+').then(r=>r.status)')==403
    click('服务帮助'); batch(['wait','400']); shot('embedded-page')
    assert js('document.querySelector(".custom-page iframe")?.getAttribute("src")')==external+'/help'
    assert js('document.querySelector(".custom-page-toolbar")') is None
    assert any(v['path']=='/help' for v in received), 'External iframe did not load'
    original_url=batch(['get','url'])[0]['url']
    click('充值中心','link')
    tabs=batch(['tab','list'])[0]['tabs'];assert len(tabs)==2, 'Link did not open a new tab'
    batch(['tab','1'],['wait','400']);expect('自定义页面已打开')
    assert batch(['get','url'])[0]['url']==external+'/topup'
    batch(['tab','close'],['tab','0'])
    assert batch(['get','url'])[0]['url']==original_url, 'Original workspace was replaced'
    # The target site's own favicon can have its own Referer; the workspace
    # origin and credentials must never be forwarded by either opening mode.
    assert all(not v['cookie'] and not v['authorization'] and not v['workspace_referer'] for v in received), 'Workspace credentials or referrer forwarded'
    batch(['set','viewport','390','844']);shot('member-embedded-mobile')
    assert not js('document.documentElement.scrollWidth>innerWidth'), 'Mobile iframe overflow'

    SESSION='huixu-menus-admin-qa'
    batch(['uncheck',ref('启用菜单','checkbox')]);click('保存自定义菜单');expect('自定义菜单已保存')
    click('删除菜单 2');click('保存自定义菜单');expect('自定义菜单已保存')
    SESSION='huixu-menus-member-qa'
    assert js('fetch('+json.dumps('/workspace/custom/'+topup_id+'/open')+').then(r=>r.status)')==404
    assert api('/api/workspace/custom-menus/'+private)['status']==404
    batch(['open',BASE+'/workspace/custom/'+help_id],['wait','400'])
    expect('服务帮助');assert api('/api/workspace/navigation')['status']==200
    print('PASS: menu CRUD/order/SVG, role boundaries, iframe, real new-tab click, revocation, mobile layouts',flush=True)
    (SHOTS/'result.json').write_text(json.dumps({'passed':True,'external_real_service_contacted':False,'credentials_forwarded':False}),encoding='utf-8')
except Exception:
    shot('failure')
    raise
finally:
    server.shutdown();server.server_close()
    for SESSION in ['huixu-menus-admin-qa','huixu-menus-member-qa']:
        try:batch(['close'])
        except Exception:pass

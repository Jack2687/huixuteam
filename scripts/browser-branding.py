"""Brand icon upload and permission QA against a fresh database on port 18743."""
import json
import os
import secrets
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CONFIG = json.loads((ROOT / '.local/branding-qa-config.json').read_text(encoding='utf-8'))
assert '/huixu_branding_qa_' in CONFIG['DATABASE_URL'] and CONFIG['PORT'] == '18743'
BASE = 'http://127.0.0.1:18743'
BROWSER = Path(os.environ['APPDATA']) / 'npm/node_modules/agent-browser/bin/agent-browser-win32-x64.exe'
SESSION = 'huixu-branding-qa'
SHOTS = ROOT / '.local/branding-qa'
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
    batch(['set','viewport','1440','1000'],['wait','.account-access input[type="email"]'])
    batch(['wait','--fn','Array.from(document.querySelectorAll(".brand-mark")).every(v=>v.complete&&v.naturalWidth>0)'])
    shot('default-login')
    assert js('document.querySelector("link[rel=icon]").getAttribute("href").startsWith("/api/public/brand-icon")')
    login(CONFIG['BOOTSTRAP_EMAIL'],CONFIG['BOOTSTRAP_PASSWORD'])
    batch(['wait','.topbar-console']);click('运营控制台');batch(['wait','.admin-panel']);batch(['click',ref('站点设置','tab')]);click('品牌与服务')
    batch(['wait','.brand-settings-card'])
    assert '回序默认图标' in batch(['get','text','body'])[0]['text']
    shot('branding-desktop')
    batch(['set','viewport','390','844']);assert not js('document.documentElement.scrollWidth>innerWidth');shot('branding-mobile')
    batch(['set','viewport','1440','1000'])
    fixture=SHOTS/'upload-test.svg'
    fixture.write_text('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" rx="16" fill="#6f5289"/><circle cx="32" cy="32" r="17" fill="#fff2dc"/></svg>',encoding='utf-8')
    default=api('/api/admin/site-settings/branding')['data']
    batch(['upload','input[aria-label="上传站点图标"]',str(fixture)],['wait','.brand-file-name'])
    assert api('/api/admin/site-settings/branding')['data']==default, 'Selection persisted before save'
    click('保存图标');batch(['wait','--text','站点图标已保存'])
    custom=api('/api/admin/site-settings/branding')['data']
    assert custom['customized'] and custom['icon_url']!=default['icon_url']
    assert js('document.querySelector("link[rel=icon]").getAttribute("href")')==custom['icon_url']
    assert js('document.querySelector(".sidebar .brand-mark").getAttribute("src")')==custom['icon_url']
    shot('branding-uploaded')
    batch(['open',BASE+'/#admin'],['wait','.admin-panel']);batch(['click',ref('站点设置','tab')]);click('品牌与服务')
    batch(['wait','.brand-settings-card']);assert api('/api/admin/site-settings/branding')['data']==custom
    click('恢复默认图标');batch(['wait','--text','已恢复回序默认图标'])
    assert api('/api/admin/site-settings/branding')['data']==default
    # Browser preview accepts static images; the server must reject SVG scripts.
    bad=SHOTS/'bad.svg';bad.write_text('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><script>alert(1)</script><rect width="24" height="24"/></svg>',encoding='utf-8')
    batch(['upload','input[aria-label="上传站点图标"]',str(bad)],['wait','.brand-file-name']);click('保存图标')
    batch(['wait','.brand-settings-card [role=alert]'])
    assert api('/api/admin/site-settings/branding')['data']==default,'Invalid SVG changed saved branding'
    click('取消选择')
    email='brand-member-'+secrets.token_hex(4)+'@test.invalid';password=secrets.token_urlsafe(24)
    assert api('/api/admin/users','POST',{'email':email,'name':'品牌验收用户','password':password,'role':'member','service_fee':3000})['status']==200
    click('退出账户');batch(['wait','.account-access input[type="email"]']);js('performance.clearResourceTimings()')
    login(email,password);assert_member_surface()
    for method in ('GET','PUT','DELETE'):
        assert api('/api/admin/site-settings/branding',method)['status']==403
    assert api('/api/public/site')['data']['branding']==default
    assert js('document.querySelector(".brand-mark").getAttribute("src")')==default['icon_url']
    shot('member-default-icon')
    (SHOTS/'result.json').write_text(json.dumps({'passed':True,'default_icon_visible':True,'preview_without_save':True,'svg_upload_and_reload':True,'reset':True,'unsafe_svg_rejected':True,'favicon_and_sidebar_updated':True,'non_admin_write_denied':True,'mobile_no_overflow':True}),encoding='utf-8')
    print('PASS: branding preview/upload/save/reload/reset, favicon/sidebar/login, SVG validation, non-admin permissions, mobile layout',flush=True)
except Exception:
    try:shot('failure')
    except Exception:pass
    raise
finally:
    try:batch(['close'])
    except Exception:pass

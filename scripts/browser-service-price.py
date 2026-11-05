"""Registration service-price UI QA against a fresh database on port 18743."""
import json
import os
import secrets
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CONFIG = json.loads((ROOT / '.local/finance-qa-config.json').read_text(encoding='utf-8'))
assert '/huixu_finance_qa_' in CONFIG['DATABASE_URL'] and CONFIG['PORT'] == '18743'
BASE = 'http://127.0.0.1:18743'
BROWSER = Path(os.environ['APPDATA']) / 'npm/node_modules/agent-browser/bin/agent-browser-win32-x64.exe'
SESSION = 'huixu-finance-qa'
SHOTS = ROOT / '.local/finance-qa'
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


try:
    subprocess.run([str(BROWSER),'--session',SESSION,'open',BASE],stdout=subprocess.DEVNULL,timeout=45,check=True)
    batch(['set','viewport','1440','1000'],['wait','.account-access input[type="email"]'])
    login(CONFIG['BOOTSTRAP_EMAIL'],CONFIG['BOOTSTRAP_PASSWORD'])
    batch(['wait','.topbar-console']);click('运营控制台');batch(['wait','.admin-panel']);batch(['click',ref('站点设置','tab')]);click('品牌与服务')
    batch(['wait','.service-pricing input:not(:disabled)'])
    assert js('Number(document.querySelector(".service-pricing input").value)')==100
    batch(['fill',ref('注册默认服务单价','spinbutton'),'123.45']);click('保存服务单价');batch(['wait','--text','注册默认服务单价已保存'])
    assert api('/api/admin/site-settings/service')['data']['default_service_fee']==12345
    batch(['open',BASE+'/#admin'],['wait','.admin-panel']);batch(['click',ref('站点设置','tab')]);click('品牌与服务');batch(['wait','.service-pricing input:not(:disabled)'])
    assert js('Number(document.querySelector(".service-pricing input").value)')==123.45
    batch(['fill',ref('注册默认服务单价','spinbutton'),'100']);click('保存服务单价')
    batch(['wait','--fn','document.querySelector(".service-pricing button").disabled===false'])
    assert api('/api/admin/site-settings/service')['data']['default_service_fee']==10000
    shot('service-price-desktop');batch(['set','viewport','390','844']);assert not js('document.documentElement.scrollWidth>innerWidth');shot('service-price-mobile')
    (SHOTS/'result.json').write_text(json.dumps({'default_100':True,'save_and_reload':True,'restored_100':True,'mobile_layout':True}))
    print('SERVICE_PRICE_BROWSER_QA_PASSED')
finally:
    subprocess.run([str(BROWSER),'--session',SESSION,'close'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=20)

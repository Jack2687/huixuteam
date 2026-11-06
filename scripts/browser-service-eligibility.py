"""Service eligibility confirmation QA against a fresh database on port 18743."""
import json
import os
import secrets
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CONFIG = json.loads((ROOT / '.local/eligibility-qa-config.json').read_text(encoding='utf-8'))
assert '/huixu_eligibility_qa_' in CONFIG['DATABASE_URL'] and CONFIG['PORT'] == '18743'
BASE = 'http://127.0.0.1:18743'
BROWSER = Path(os.environ['APPDATA']) / 'npm/node_modules/agent-browser/bin/agent-browser-win32-x64.exe'
SESSION = 'huixu-eligibility-qa'
SHOTS = ROOT / '.local/eligibility-qa'
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
    notice='本平台仅面向有退款办理经验、熟悉完整流程的业务用户，暂不接受个人用户使用。'
    assert notice in js('document.querySelector(".login-art").textContent')
    shot('login-desktop')
    click('注册新账户')
    batch(['wait','input[name="service_eligibility_accepted"]'])
    assert notice in js('document.querySelector(".login-art").textContent')
    assert js('document.querySelector("input[name=service_eligibility_accepted]").checked') is False
    assert js('document.querySelector("input[name=service_eligibility_accepted]").required') is True
    assert js('document.querySelector(".account-access button.full").disabled') is True
    address='eligibility-'+secrets.token_hex(4)+'@test.invalid'
    password=secrets.token_urlsafe(20)
    for label,value in [('显示名称','业务用户验收'),('账户邮箱',address),('设置密码',password),('确认密码',password)]:batch(['fill',ref(label,'textbox'),value])
    js('document.querySelector(".account-access form").dispatchEvent(new Event("submit",{bubbles:true,cancelable:true}))')
    batch(['wait','--text','请先确认你有退款办理经验'])
    shot('registration-unchecked')
    batch(['check','input[name="service_eligibility_accepted"]'])
    assert js('document.querySelector(".account-access button.full").disabled') is False
    batch(['uncheck','input[name="service_eligibility_accepted"]'])
    assert js('document.querySelector(".account-access button.full").disabled') is True
    batch(['check','input[name="service_eligibility_accepted"]'])
    shot('registration-desktop')
    batch(['set','viewport','390','844'])
    assert not js('document.documentElement.scrollWidth>innerWidth')
    batch(['scrollintoview','input[name="service_eligibility_accepted"]']);shot('registration-mobile')
    click('创建账户');batch(['wait','--text','注册成功，请使用新账号登录。'])
    assert js('document.querySelector("input[name=service_eligibility_accepted]")===null')
    click('注册新账户')
    assert js('document.querySelector("input[name=service_eligibility_accepted]").checked') is False
    # The CLI's click does not reliably scroll a below-fold target on mobile.
    js('document.querySelector(".auth-links > button").scrollIntoView({block:"center"})')
    click('返回登录');batch(['wait','.account-access input[autocomplete="current-password"]']);login(address,password)
    batch(['wait','.service-terms-callout'])
    assert notice in js('document.querySelector(".service-terms-callout .service-eligibility").textContent')
    assert js('document.querySelector(".service-eligibility").getBoundingClientRect().top>document.querySelector(".service-terms-callout p").getBoundingClientRect().bottom')
    assert not js('document.documentElement.scrollWidth>innerWidth');shot('home-mobile')
    batch(['set','viewport','1440','1000']);shot('home-desktop')
    (SHOTS/'result.json').write_text(json.dumps({'passed':True,'login_and_register_notice':True,'confirmation_default_unchecked':True,'local_bypass_rejected':True,'register_checked_success':True,'resets_after_switch':True,'home_notice_below_terms':True,'mobile_layout':True}))
    print('ELIGIBILITY_BROWSER_QA_PASSED')
except Exception:
    print(batch(['snapshot','-i'])[0],flush=True)
    shot('failure')
    raise
finally:
    subprocess.run([str(BROWSER),'--session',SESSION,'close'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=20)

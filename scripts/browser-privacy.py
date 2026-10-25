"""CDP response audit on isolated QA 18743; no external account operations.

Prepare scripts/testdata/privacy_fixture.go against llm_refund_qa first.
Responses are checked in memory; no credential-bearing HAR is written.
"""
from pathlib import Path
import threading
from curl_cffi import requests

root = Path(__file__).resolve().parent.parent
helpers = root / 'scripts/browser-smoke.py'
scope = {'__file__': str(helpers)}
exec(compile(helpers.read_text(encoding='utf-8').split('\ntry:\n', 1)[0], str(helpers), 'exec'), scope)
globals().update(scope)
SESSION = 'huixu-privacy-qa'
scope['SESSION'] = SESSION
ARTIFACTS = ROOT / '.local' / 'privacy-qa'
ARTIFACTS.mkdir(exist_ok=True)
scope['ARTIFACTS'] = ARTIFACTS
fixture = json.loads((ROOT / '.local/privacy-fixture.json').read_text(encoding='utf-8'))
PRIVATE = ('opaque-protocol-canary', 'export_and_deactivate', 'backend-api/accounts',
           'auth.openai.com', 'chatgpt-account-id', 'merge_v2', 'invalid_workspace_selected',
           'child_user_id', 'accepted_at', 'lease_token', 'curl_cffi', 'sourceMappingURL',
           'private-child-password', 'JBSWY3DPEHPK3PXP', 'draft-private-canary', '导出停用', '移出团队')

class NetworkAudit:
    def __init__(self):
        endpoint = subprocess.run(COMMAND + ['--session',SESSION,'get','cdp-url'], capture_output=True,encoding='utf-8',timeout=15,check=True).stdout.strip()
        self.client = requests.Session()
        self.ws = self.client.ws_connect(endpoint)
        self.counter = 0
        self.session = None
        targets = self.call('Target.getTargets')['targetInfos']
        page = next(t for t in targets if t['type']=='page' and t['url'].startswith(BASE))
        self.session = self.call('Target.attachToTarget',{'targetId':page['targetId'],'flatten':True})['sessionId']
        self.call('Network.enable',{'maxTotalBufferSize':20000000,'maxResourceBufferSize':5000000})
        self.call('Network.setCacheDisabled',{'cacheDisabled':True})
        self.urls = []; self.responses = {}; self.pending = {}; self.bodies = []; self.errors = []; self.stopped=False
        self.thread=threading.Thread(target=self.read,daemon=True); self.thread.start()
    def call(self,method,params=None):
        self.counter+=1
        msg={'id':self.counter,'method':method,'params':params or {}}
        if self.session: msg['sessionId']=self.session
        self.ws.send_json(msg)
        while True:
            result=json.loads(self.ws.recv()[0])
            if result.get('id')==self.counter:
                assert 'error' not in result, 'CDP initialization failed'
                return result.get('result',{})
    def read(self):
        try:
            while not self.stopped:
                m=json.loads(self.ws.recv()[0]); p=m.get('params',{}); method=m.get('method','')
                if method=='Network.requestWillBeSent':self.urls.append(p['request']['url'])
                elif method=='Network.responseReceived':self.responses[p['requestId']]=p['response']
                elif method=='Network.loadingFinished' and p['requestId'] in self.responses:
                    response=self.responses[p['requestId']]
                    if any(kind in response.get('mimeType','') for kind in ('json','javascript','html','css','text')):
                        self.counter+=1;self.pending[self.counter]=response['url']
                        self.ws.send_json({'id':self.counter,'method':'Network.getResponseBody','params':{'requestId':p['requestId']},'sessionId':self.session})
                elif m.get('id') in self.pending:
                    url=self.pending.pop(m['id'])
                    if 'error' in m:self.errors.append('response unavailable: '+url)
                    else:
                        data=m['result'];body=data['body']
                        if data.get('base64Encoded'):body=base64.b64decode(body).decode('utf-8')
                        self.bodies.append((url,body))
        except Exception:
            if not self.stopped:self.errors.append('CDP capture interrupted')
    def check(self):
        for _ in range(30):
            if not self.pending:break
            time.sleep(.1)
        assert not self.errors,self.errors
        assert len(self.bodies)>20,'insufficient captured responses'
        assert all(url.startswith(BASE) for url in self.urls if url.startswith(('http:','https:'))),'browser contacted another host'
        for url,body in self.bodies:
            assert not any(word in body for word in PRIVATE),'private content in response: '+url
        assert any('/assets/admin-console-' in url for url,_ in self.bodies),'admin bundle not inspected'
        assert any('/assets/admin-execution-' in url for url,_ in self.bodies),'execution bundle not inspected'
        assert any('/api/admin/refunds/' in url for url,_ in self.bodies),'admin details not inspected'
        return len(self.urls),len(self.bodies)
    def close(self):
        self.stopped=True
        self.ws.close();self.thread.join(2);self.client.close()

def request(path):return evaluate('fetch('+json.dumps(path)+').then(async r=>({status:r.status,data:await r.text()}))')
def clean_page():assert not any(word in text() for word in PRIVATE),'private page content'

capture=None
try:
    subprocess.run(COMMAND+['--session',SESSION,'open',BASE],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=45,check=True)
    capture=NetworkAudit()
    batch(['open',BASE],['set','viewport','1440','950'])
    expect('登录页公告：上线说明')
    assert '登录页不可见的验收草稿' not in text()
    assert evaluate('window.loginAnnouncementXSS===undefined')
    def login_title():return evaluate('document.querySelector(".login-announcement-card[aria-hidden=false] h3").textContent')
    assert login_title()=='登录页公告：上线说明'
    click('下一条公告');batch(['wait','400']);assert login_title()=='登录页公告：服务指引'
    evaluate('document.querySelector(".login-announcement-card[aria-hidden=false]").focus()')
    batch(['press','ArrowLeft'],['wait','400']);assert login_title()=='登录页公告：上线说明'
    shot('login-announcements-desktop')
    batch(['set','viewport','390','844'],['wait','300'])
    assert not evaluate('document.documentElement.scrollWidth>innerWidth')
    batch(['scroll','right','340','--selector','.login-announcement-slides'],['wait','400'])
    assert login_title()=='登录页公告：服务指引'
    shot('login-announcements-mobile')
    batch(['set','viewport','1440','950'])
    fill('登录邮箱',fixture['email']);fill('账户密码',fixture['password']);click('进入服务台');expect('保密验收，欢迎回来。')
    assert evaluate('document.querySelector(".service-terms-callout p").textContent')=='费用按服务办理结果结算，不以退款到账为依据。申请尚未开始时可撤回，开始后可在详情跟踪。'
    assert evaluate('parseFloat(getComputedStyle(document.querySelector(".service-terms-callout p")).fontSize)>=22')
    shot('overview-terms-desktop')
    batch(['set','viewport','390','844']);shot('overview-terms-mobile')
    assert not evaluate('document.documentElement.scrollWidth>innerWidth')
    batch(['set','viewport','1440','950'])
    click('申请记录')
    for status in ('succeeded','attention'):
        click('查看 '+fixture[status]['email']+' 的申请');expect('服务单详情');clean_page();shot('member-'+status);click('关闭')
    submitted='browser-'+str(int(time.time()))+'@example.test'
    click('提交申请');fill('服务账号邮箱',submitted)
    fill('服务账号密码','private-child-password');fill('验证器密钥（2FA）','JBSWY3DPEHPK3PXP')
    batch(['check',ref('我有权使用以上账号，并授权服务方使用所填资料办理本次退款服务。','checkbox')])
    click('确认提交 1 笔');expect(submitted);click('查看 '+submitted+' 的申请');expect('申请已受理');clean_page()
    click('撤回申请');click('确认撤回');expect('申请已取消，冻结积分已释放。');clean_page();click('关闭')
    for path in ('/api/dashboard','/api/notifications','/api/billing'):
        assert request(path)['status']==200
    assert request('/api/admin/refund-settings')['status']==403
    click('退出账户');fill('登录邮箱',CONFIG['BOOTSTRAP_EMAIL']);fill('账户密码',CONFIG['BOOTSTRAP_PASSWORD']);click('进入服务台');expect('Admin，欢迎回来。')
    click('运营控制台');click('服务资源与调度','tab');expect('单个母号依次办理');clean_page()
    assert json.loads(request('/api/admin/refund-settings')['data'])['settings']['enabled'] is False
    click('申请清单','tab')
    for status in ('succeeded','attention'):
        evaluate('(()=>{const row=[...document.querySelectorAll("tbody tr")].find(r=>r.textContent.includes('+json.dumps(fixture[status]['email'])+'));if(!row)throw Error("fixture row missing");row.querySelector("button").click()})()')
        expect('服务单详情');clean_page();shot('admin-'+status);click('关闭')
    for path in ('/api/admin/audit','/api/admin/overview','/api/admin/refunds/'+fixture['attention']['id']):assert request(path)['status']==200
    for path in ('/runtime/refund_runner.py','/.env','/.local/server.err.log','/docs/REFUND-INTEGRATION.md','/assets/index.js.map'):assert request(path)['status']==404
    # Inspect every admin bundle even when a fixture has no recovery controls.
    for path in (ROOT/'webui/static/assets').glob('admin-*.js'):
        assert request('/assets/'+path.name)['status']==200
    batch(['wait','300'])
    counts=capture.check()
    (ARTIFACTS/'result.json').write_text(json.dumps({'passed':True,'requests':counts[0],'response_bodies':counts[1],'external_requests':0,'private_matches':0,'scope':'QA fixtures; no real upstream operations'},indent=2),encoding='utf-8')
    print(f'PASS: {counts[0]} browser requests, {counts[1]} captured response bodies; member/admin/API data and all admin bundles; no core protocol or external browser requests.',flush=True)
    for item in fixture['announcements']:
        assert evaluate('fetch('+json.dumps('/api/admin/announcements/'+item)+',{method:"DELETE"}).then(r=>r.status)')==200
finally:
    if capture:capture.close()
    try:batch(['close'])
    except Exception:pass

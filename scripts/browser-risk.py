"""Risk UI and response audit in isolated QA only. No upstream operations."""
from pathlib import Path
import threading
from curl_cffi import requests

helpers = Path(__file__).with_name('browser-smoke.py')
scope = {'__file__': str(helpers)}
exec(compile(helpers.read_text(encoding='utf-8').split('\ntry:\n', 1)[0], str(helpers), 'exec'), scope)
globals().update(scope)
SESSION = 'huixu-risk-qa'; scope['SESSION'] = SESSION
ARTIFACTS = ROOT / '.local/risk-qa'; ARTIFACTS.mkdir(exist_ok=True); scope['ARTIFACTS'] = ARTIFACTS
fixture = json.loads((ROOT / '.local/risk-fixture.json').read_text(encoding='utf-8'))
PRIVATE = ('opaque-protocol-canary','export_and_deactivate','backend-api/accounts','auth.openai.com','chatgpt-account-id','merge_v2','invalid_workspace_selected','child_user_id','accepted_at','lease_token','curl_cffi','sourceMappingURL','private-child-password','JBSWY3DPEHPK3PXP','导出停用','移出团队',fixture['key'])
privacy = Path(__file__).with_name('browser-privacy.py').read_text(encoding='utf-8')
exec(privacy[privacy.index('class NetworkAudit:'):privacy.index('\ndef request(')], globals())

def select(label,value): batch(['select',ref(label,'combobox'),value],['wait','200'])
def request(path, method='GET', body=None):
    opts = {'method':method}
    if body is not None: opts.update(headers={'Content-Type':'application/json'},body=json.dumps(body))
    return evaluate('fetch('+json.dumps(path)+','+json.dumps(opts)+').then(async r=>({status:r.status,data:await r.json()}))')
def has_events(): batch(['wait','--fn','document.querySelectorAll(".risk-card tbody tr .risk-badge").length>0'])

capture=None
try:
    subprocess.run(COMMAND+['--session',SESSION,'open',BASE],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=45,check=True)
    capture=NetworkAudit()
    batch(['open',BASE],['set','viewport','1500','1050'])
    fill('登录邮箱',CONFIG['BOOTSTRAP_EMAIL']);fill('账户密码',CONFIG['BOOTSTRAP_PASSWORD']);click('进入服务台')
    expect('Admin，欢迎回来。');click('运营控制台');click('风控监控','tab');expect('关注异常访问')
    fill('风控搜索',fixture['email']);has_events();expect('申请排队时间较长');expect('来源未核实');shot('risk-desktop')
    select('事件分类','application');select('事件处理状态','pending');has_events()
    click('标记已处理');expect('事件已标记处理');select('事件处理状态','resolved');has_events();expect('已处理')
    select('事件处理状态','');select('事件分类','')
    click('当前限制');expect(fixture['email']);click('解除限制');expect('后续请求将重新计数');click('确认操作');expect('当前没有生效的访问限制')
    click('配置客户');fill('查找风控客户',fixture['email']);batch(['wait','350']);select('选择风控客户',fixture['user_id'])
    expect('暂停该客户提交新申请');batch(['check',ref('暂停该客户提交新申请','checkbox')]);fill('客户查询与轮询次数','650','spinbutton');click('保存客户规则');expect('客户风控规则已保存');expect('已暂停')
    control=request('/api/admin/risk-customers/'+fixture['user_id'])['data'];assert control['paused'] and control['limits']['query']==650
    click('调整 / 恢复');batch(['uncheck',ref('暂停该客户提交新申请','checkbox')]);click('保存客户规则');expect('客户风控规则已保存');expect('正常')
    click('配置');expect('风控配置');assert evaluate('document.querySelector("select[aria-label=查询与轮询处理方式]").value')=='observe'
    fill('单客户排队数量告警','25','spinbutton');select('提交申请处理方式','enforce');fill('提交申请最大次数','40','spinbutton');click('保存风控配置');expect('风控配置已保存')
    batch(['open',BASE+'/?riskreload='+str(int(time.time()))+'#admin']);expect('运营概况');click('风控监控','tab');expect('关注异常访问');click('配置');expect('风控配置')
    assert evaluate('document.querySelector("input[aria-label=单客户排队数量告警]").value')=='25'
    assert evaluate('document.querySelector("select[aria-label=提交申请处理方式]").value')=='enforce'
    shot('risk-settings-desktop');click('填入推荐默认值');expect('保存后才生效');click('保存风控配置');expect('风控配置已保存')
    batch(['set','viewport','390','844']);shot('risk-settings-mobile');assert not evaluate('document.documentElement.scrollWidth>innerWidth')
    click('返回监控');expect('关注异常访问');shot('risk-mobile');assert not evaluate('document.documentElement.scrollWidth>innerWidth')
    batch(['set','viewport','1500','1050']);fill('风控搜索',fixture['email']);click('访问统计');expect('风控验收密钥');click('撤销密钥');expect('此操作不可撤回');click('确认操作');expect('操作已完成')
    click('配置');fill('监控页面刷新间隔（秒）','5','spinbutton');click('保存风控配置');expect('风控配置已保存');click('返回监控')
    evaluate('window.riskCalls=0;window.riskFetch=window.fetch;window.fetch=(...args)=>{if(String(args[0]).includes("/api/admin/risk-monitor"))window.riskCalls++;return window.riskFetch(...args)}')
    batch(['wait','--fn','window.riskCalls>0']);click('配置');before=evaluate('window.riskCalls');batch(['wait','5300']);assert evaluate('window.riskCalls')==before
    click('返回监控');batch(['uncheck',ref('自动刷新','checkbox')]);before=evaluate('window.riskCalls');batch(['wait','5300']);assert evaluate('window.riskCalls')==before
    click('刷新');assert evaluate('window.riskCalls')>before
    click('账户管理','tab');before=evaluate('window.riskCalls');batch(['wait','5300']);assert evaluate('window.riskCalls')==before
    # Inspect every captured response, not just rendered text. No HAR with credentials.
    for _ in range(30):
        if not capture.pending:break
        time.sleep(.1)
    assert not capture.errors,capture.errors
    assert all(url.startswith(BASE) for url in capture.urls if url.startswith(('http:','https:')))
    assert any('/assets/admin-risk-' in url for url,_ in capture.bodies)
    assert any('/api/admin/risk-monitor' in url for url,_ in capture.bodies)
    for url,body in capture.bodies:assert not any(p in body for p in PRIVATE),'private response: '+url
    assert not batch(['errors'])[0].get('errors'),'browser runtime errors'
    result={'browser_risk':'passed','requests':len(capture.urls),'responses':len(capture.bodies),'privacy':'passed','desktop_mobile':'passed'}
    (ARTIFACTS/'result.json').write_text(json.dumps(result),encoding='utf-8');print(json.dumps(result),flush=True)
finally:
    if capture:capture.close()
    batch(['close'])

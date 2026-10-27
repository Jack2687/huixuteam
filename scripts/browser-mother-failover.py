"""Isolated QA: configuration persistence, role boundaries and browser responses."""
from pathlib import Path
import threading
from curl_cffi import requests

root=Path(__file__).resolve().parent.parent
helper=root/'scripts/browser-smoke.py'
exec(compile(helper.read_text(encoding='utf-8').split('\ntry:\n',1)[0],str(helper),'exec'))
SESSION='huixu-mother-failover-qa'
ARTIFACTS=ROOT/'.local/mother-failover-qa';ARTIFACTS.mkdir(exist_ok=True)
source=(root/'scripts/browser-privacy.py').read_text(encoding='utf-8')
exec(compile(source[source.index('class NetworkAudit:'):source.index('\ndef request(')],'network-audit','exec'))
fixture=json.loads((ROOT/'.local/recovery-fixture.json').read_text(encoding='utf-8'))
PRIVATE=('opaque-protocol-canary','export_and_deactivate','backend-api/accounts','auth.openai.com','chatgpt-account-id','merge_v2','invalid_workspace_selected','child_user_id','accepted_at','lease_token','curl_cffi','sourceMappingURL','private-child-password','JBSWY3DPEHPK3PXP','private-child-id','private-mother-token','private-proxy','导出停用','移出团队','failed_mothers','auth_failed_fingerprint')
def request(path):return evaluate('fetch('+json.dumps(path)+').then(async r=>({status:r.status,data:await r.text()}))')
capture=None
try:
    subprocess.run(COMMAND+['--session',SESSION,'open',BASE],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=45,check=True)
    capture=NetworkAudit();batch(['open',BASE],['set','viewport','1440','1100'])
    assert request('/api/admin/refund-settings')['status']==401
    fill('登录邮箱',fixture['email']);fill('账户密码',fixture['password']);click('进入服务台');expect('流程验收，欢迎回来。')
    assert request('/api/admin/refund-settings')['status']==403
    click('退出账户');fill('登录邮箱',CONFIG['BOOTSTRAP_EMAIL']);fill('账户密码',CONFIG['BOOTSTRAP_PASSWORD']);click('进入服务台');expect('Admin，欢迎回来。')
    click('运营控制台');click('服务资源与调度','tab');expect('母号异常自动换号')
    original=json.loads(request('/api/admin/refund-settings')['data'])['settings']
    assert original['enabled'] is False
    fill('每笔申请最多换母号次数','2','spinbutton');fill('换母号前等待（秒）','45','spinbutton')
    batch(['uncheck',ref('开始办理前母号不可用时，重新排队换母号','checkbox')])
    click('应用调度配置');expect('执行设置已保存');click('读取状态')
    cfg=json.loads(request('/api/admin/refund-settings')['data'])['settings']
    assert cfg['mother_failover']=={'enabled':False,'max_switches':2,'retry_seconds':45}
    assert {k:v for k,v in cfg.items() if k!='mother_failover'}=={k:v for k,v in original.items() if k!='mother_failover'}
    batch(['check',ref('开始办理前母号不可用时，重新排队换母号','checkbox')])
    fill('每笔申请最多换母号次数','3','spinbutton');fill('换母号前等待（秒）','30','spinbutton')
    click('应用调度配置');expect('执行设置已保存');click('读取状态');shot('settings-desktop')
    cfg=json.loads(request('/api/admin/refund-settings')['data'])['settings']
    assert cfg['mother_failover']=={'enabled':True,'max_switches':3,'retry_seconds':30}
    batch(['set','viewport','390','844']);assert not evaluate('document.documentElement.scrollWidth>innerWidth');shot('settings-mobile')
    batch(['set','viewport','1440','1100'])
    assert request('/api/admin/refunds/'+fixture['attention']['id']+'/timeline')['status']==200
    for path in ['/.env','/runtime/refund_runner.py','/runtime/refund_recovery.py','/internal/store/refund_mother_failover.go','/docs/REFUND-INTEGRATION.md']:
        assert request(path)['status']==404
    counts=capture.check()
    assert not any('/resume' in u or '/cancel' in u or '/refund-mothers/check' in u for u in capture.urls)
    result={'passed':True,'requests':counts[0],'response_bodies':counts[1],'external_requests':0,'private_matches':0,'scope':'isolated QA, worker disabled, no upstream operations'}
    (ARTIFACTS/'result.json').write_text(json.dumps(result,indent=2),encoding='utf-8');print(json.dumps(result),flush=True)
finally:
    if capture:capture.close()
    try:batch(['close'])
    except Exception:pass

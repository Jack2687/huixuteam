"""Recovery UI and real browser network audit against disabled-worker QA only."""
from pathlib import Path
import threading
from curl_cffi import requests

root=Path(__file__).resolve().parent.parent
helper=root/'scripts/browser-smoke.py'
exec(compile(helper.read_text(encoding='utf-8').split('\ntry:\n',1)[0],str(helper),'exec'))
SESSION='huixu-recovery-qa'
ARTIFACTS=ROOT/'.local/recovery-qa';ARTIFACTS.mkdir(exist_ok=True)
source=(root/'scripts/browser-privacy.py').read_text(encoding='utf-8')
exec(compile(source[source.index('class NetworkAudit:'):source.index('\ndef request(')],'network-audit','exec'))
fixture=json.loads((ROOT/'.local/recovery-fixture.json').read_text(encoding='utf-8'))
PRIVATE=('opaque-protocol-canary','export_and_deactivate','backend-api/accounts','auth.openai.com','chatgpt-account-id','merge_v2','invalid_workspace_selected','child_user_id','accepted_at','lease_token','curl_cffi','sourceMappingURL','private-child-password','JBSWY3DPEHPK3PXP','private-child-id','private-mother-token','private-proxy','导出停用','移出团队')
path='/api/admin/refunds/'+fixture['attention']['id']+'/timeline'
def request(url):return evaluate('fetch('+json.dumps(url)+').then(async r=>({status:r.status,data:await r.text()}))')
capture=None
try:
 subprocess.run(COMMAND+['--session',SESSION,'open',BASE],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=45,check=True)
 capture=NetworkAudit();batch(['open',BASE],['set','viewport','1440','1100'])
 assert request(path)['status']==401
 fill('登录邮箱',fixture['email']);fill('账户密码',fixture['password']);click('进入服务台');expect('流程验收，欢迎回来。')
 assert request(path)['status']==403
 click('申请记录');click('查看 '+fixture['attention']['email']+' 的申请');expect('服务单详情')
 assert '办理流程记录' not in text() and '个人空间' not in text();click('关闭');click('退出账户')
 fill('登录邮箱',fixture['operator_email']);fill('账户密码',fixture['password']);click('进入服务台');expect('流程运营验收，欢迎回来。')
 assert request(path)['status']==403
 for p in (ROOT/'webui/static/assets').glob('admin-timeline-*'):assert request('/assets/'+p.name)['status']==403
 click('退出账户');fill('登录邮箱',CONFIG['BOOTSTRAP_EMAIL']);fill('账户密码',CONFIG['BOOTSTRAP_PASSWORD']);click('进入服务台');expect('Admin，欢迎回来。')
 click('运营控制台');click('服务资源与调度','tab');expect('异常恢复与占用告警')
 assert evaluate('document.querySelector("input[type=checkbox]").checked') is not True
 fill('自动重试次数（首次失败之后）','4','spinbutton');fill('核实各轮间隔（秒，逗号分隔）','30, 60, 120, 300');fill('清理各轮间隔（秒，逗号分隔）','15, 30, 60');fill('异常占用告警阈值（秒）','600','spinbutton')
 click('应用调度配置');expect('执行设置已保存');click('读取状态')
 config=json.loads(request('/api/admin/refund-settings')['data'])['settings']
 assert config['enabled'] is False and config['recovery']['max_retries']==4
 fill('自动重试次数（首次失败之后）','3','spinbutton');click('应用调度配置');shot('settings-desktop')
 batch(['set','viewport','390','844']);assert not evaluate('document.documentElement.scrollWidth>innerWidth');shot('settings-mobile')
 batch(['set','viewport','1440','1100']);click('申请全监控','tab');expect('笔申请异常占用超时');click('立即跟进')
 fill('搜索监控申请',fixture['attention']['email'],'searchbox');batch(['wait','500'])
 click('监控详情 '+fixture['attention']['email']);expect('办理流程记录');expect('自动核实');expect('已核实 2 轮');expect('个人空间已不可见')
 shot('recovery-desktop');dto=json.loads(request(path)['data']);assert dto['recovery']['alert'] is True
 before=len([u for u in capture.urls if u.endswith(path)]);batch(['wait','11000']);assert len([u for u in capture.urls if u.endswith(path)])>before
 batch(['set','viewport','390','844']);assert not evaluate('document.documentElement.scrollWidth>innerWidth');shot('recovery-mobile')
 click('关闭');before=len([u for u in capture.urls if u.endswith(path)]);batch(['wait','11000']);assert len([u for u in capture.urls if u.endswith(path)])==before
 for url in ('/runtime/refund_recovery.py','/internal/store/refund_recovery.go','/.env','/docs/REFUND-INTEGRATION.md'):
  assert request(url)['status']==404
 counts=capture.check()
 assert not any('/resume' in u or '/cancel' in u or '/refund-mothers/check' in u for u in capture.urls)
 result={'passed':True,'requests':counts[0],'response_bodies':counts[1],'external_requests':0,'private_matches':0,'scope':'isolated QA, execution worker unavailable, no upstream operations'}
 (ARTIFACTS/'result.json').write_text(json.dumps(result,indent=2),encoding='utf-8');print(json.dumps(result),flush=True)
finally:
 if capture:capture.close()
 try:batch(['close'])
 except Exception:pass

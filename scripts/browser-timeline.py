"""Real browser/CDP verification on QA only. No upstream calls or stored HAR secrets."""
from pathlib import Path
import threading
from curl_cffi import requests

root=Path(__file__).resolve().parent.parent
helper=root/'scripts/browser-smoke.py'
exec(compile(helper.read_text(encoding='utf-8').split('\ntry:\n',1)[0],str(helper),'exec'))
SESSION='huixu-timeline-qa'
ARTIFACTS=ROOT/'.local/timeline-qa';ARTIFACTS.mkdir(exist_ok=True)
source=(root/'scripts/browser-privacy.py').read_text(encoding='utf-8')
exec(compile(source[source.index('class NetworkAudit:'):source.index('\ndef request(')],'network-audit','exec'))
fixture=json.loads((ROOT/'.local/timeline-fixture.json').read_text(encoding='utf-8'))
PRIVATE=('opaque-protocol-canary','export_and_deactivate','backend-api/accounts','auth.openai.com','chatgpt-account-id','merge_v2','invalid_workspace_selected','child_user_id','accepted_at','lease_token','curl_cffi','sourceMappingURL','private-child-password','JBSWY3DPEHPK3PXP','导出停用','移出团队')
def request(path):return evaluate('fetch('+json.dumps(path)+').then(async r=>({status:r.status,data:await r.text(),cache:r.headers.get("Cache-Control")}))')
def timeline_path(kind):return '/api/admin/refunds/'+fixture[kind]['id']+'/timeline'
def count(kind):return sum(u.endswith(timeline_path(kind)) for u in capture.urls)
def open_detail(kind):
 fill('搜索监控申请',fixture[kind]['email'],'searchbox');batch(['wait','500'])
 click('监控详情 '+fixture[kind]['email']);expect('办理流程记录')
 batch(['wait','300'])
 assert evaluate('document.querySelectorAll(".stage-records li").length')>=8

capture=None
try:
 subprocess.run(COMMAND+['--session',SESSION,'open',BASE],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=45,check=True)
 capture=NetworkAudit();batch(['open',BASE],['set','viewport','1440','1100'])
 assert request(timeline_path('succeeded'))['status']==401
 fill('登录邮箱',fixture['email']);fill('账户密码',fixture['password']);click('进入服务台');expect('流程验收，欢迎回来。')
 assert request(timeline_path('succeeded'))['status']==403
 click('申请记录');click('查看 '+fixture['succeeded']['email']+' 的申请');expect('服务单详情')
 assert '办理流程记录' not in text();assert not evaluate('document.querySelector(".stage-monitor")!==null');click('关闭')
 for p in (ROOT/'webui/static/assets').glob('admin-timeline-*'):assert request('/assets/'+p.name)['status']==403
 click('退出账户');fill('登录邮箱',fixture['operator_email']);fill('账户密码',fixture['password']);click('进入服务台');expect('流程运营验收，欢迎回来。')
 assert request(timeline_path('succeeded'))['status']==403
 click('运营控制台');expect('申请全监控');click('申请全监控','tab')
 fill('搜索监控申请',fixture['succeeded']['email'],'searchbox');batch(['wait','500'])
 click('监控详情 '+fixture['succeeded']['email']);expect('服务单详情')
 assert '办理流程记录' not in text();assert not evaluate('document.querySelector(".stage-monitor")!==null')
 click('关闭')
 for p in (ROOT/'webui/static/assets').glob('admin-timeline-*'):assert request('/assets/'+p.name)['status']==403
 click('退出账户');fill('登录邮箱',CONFIG['BOOTSTRAP_EMAIL']);fill('账户密码',CONFIG['BOOTSTRAP_PASSWORD']);click('进入服务台');expect('Admin，欢迎回来。')
 click('运营控制台');click('申请全监控','tab');expect('申请全监控')
 open_detail('succeeded');expect('第 2 次');expect('已受理')
 rows=evaluate('[...document.querySelectorAll(".stage-mark")].map(e=>e.textContent)');assert rows==list('提候邀登登团停移结'),rows
 shot('completed-desktop');before=count('succeeded');batch(['wait','11000']);assert count('succeeded')==before,'completed detail kept polling'
 click('刷新流程记录');assert count('succeeded')==before+1
 response=request(timeline_path('succeeded'));assert response['status']==200 and 'no-store' in response['cache']
 dto=json.loads(response['data']);assert set(dto)=={'items','active','generated_at'}
 assert all(set(row)=={'stage','attempt','status','started_at','finished_at','duration_seconds','reason'} for row in dto['items'])
 batch(['set','viewport','390','844']);assert not evaluate('document.documentElement.scrollWidth>innerWidth');shot('completed-mobile')
 click('关闭');batch(['set','viewport','1440','1100'])
 open_detail('attention');expect('待核实');shot('attention-desktop')
 # Parent refresh after recovery must restart the child timeline reader. Only
 # the displayed response is changed here; no business mutation or worker runs.
 before=count('attention')
 detail_path='/api/admin/refunds/'+fixture['attention']['id']
 evaluate('window.beforeRecoveryFetch=window.fetch;window.fetch=async(url,options)=>{const r=await window.beforeRecoveryFetch(url,options);if(String(url)==='+json.dumps(detail_path)+'){const v=await r.json();v.refund.status="processing";return new Response(JSON.stringify(v),{status:200,headers:{"Content-Type":"application/json"}})}return r}')
 click('读取最新动态');batch(['wait','300']);assert count('attention')>before,'recovery did not reload stage records'
 evaluate('window.fetch=window.beforeRecoveryFetch');click('关闭')
 open_detail('legacy');expect('未记录');shot('legacy-desktop');click('关闭')
 open_detail('failed');expect('已结束');click('关闭')
 open_detail('queued');expect('排队中');click('关闭')
 open_detail('running');expect('进行中');before=count('running');batch(['wait','11000']);assert count('running')>before,'running detail did not refresh'
 click('关闭');before=count('running');batch(['wait','11000']);assert count('running')==before,'closed detail kept polling'
 # A failed GET must show a generic error and must not initiate any operation.
 open_detail('running')
 evaluate('window.timelineOriginalFetch=window.fetch;window.fetch=(url,options)=>String(url).endsWith("/timeline")?Promise.reject(new Error("private-network-canary")):window.timelineOriginalFetch(url,options)')
 click('刷新流程记录');expect('读取失败，请重新读取记录');assert 'private-network-canary' not in text()
 evaluate('window.fetch=window.timelineOriginalFetch');click('刷新流程记录');expect('进行中');click('关闭')
 for p in (ROOT/'webui/static/assets').glob('admin-*.js'):assert request('/assets/'+p.name)['status']==200
 for path in ('/runtime/refund_runner.py','/docs/REFUND-INTEGRATION.md','/.env','/api/admin/logs','/api/admin/refunds/'+fixture['succeeded']['id']+'/execution'):
  assert request(path)['status']==404
 counts=capture.check()
 # Monitor interactions must only produce GETs; audit URLs rule out business mutations.
 assert not any('/resume' in u or '/cancel' in u or '/refund-mothers/check' in u for u in capture.urls)
 result={'passed':True,'requests':counts[0],'response_bodies':counts[1],'external_requests':0,'private_matches':0,'scenarios':['completed','unknown','retries','queued','running','failed','missing_history','read_error','terminal_poll_stop','close_poll_stop','member_and_anonymous_denied','protected_assets'],'scope':'offline QA fixtures; real browser network; no upstream operations'}
 (ARTIFACTS/'result.json').write_text(json.dumps(result,indent=2),encoding='utf-8')
 print(json.dumps(result,ensure_ascii=False),flush=True)
finally:
 if capture:capture.close()
 try:batch(['close'])
 except Exception:pass

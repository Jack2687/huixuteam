"""Resource checks against isolated QA and a server-side protocol fixture only."""
from pathlib import Path
import json
source=Path(__file__).with_name('browser-privacy.py')
namespace={'__file__':str(source)}
exec(compile(source.read_text(encoding='utf-8').split('\ncapture=None',1)[0],str(source),'exec'),namespace)
globals().update(namespace)
SESSION='huixu-resource-qa';scope['SESSION']=SESSION;namespace['SESSION']=SESSION
# Helpers execute in their original globals dictionary.
for fn in [batch,snapshot,ref,click,fill,text,expect,evaluate,shot]: fn.__globals__['SESSION']=SESSION
capture=None
try:
 subprocess.run(COMMAND+['--session',SESSION,'open',BASE],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=45,check=True)
 batch(['set','viewport','1540','1040'])
 fill('登录邮箱',CONFIG['BOOTSTRAP_EMAIL']);fill('账户密码',CONFIG['BOOTSTRAP_PASSWORD']);click('进入服务台')
 expect('1 积分 = 1 R')
 capture=NetworkAudit()
 click('运营控制台');click('服务资源与调度','tab');expect('校验 / 席位（剩余 / 总数）')
 config=evaluate('fetch("/api/admin/refund-settings").then(r=>r.json())')
 assert config['mothers'] and config['proxies'],'QA resource fixtures missing'
 # Select a QA mother with an enabled dedicated proxy.
 selected=next(m for m in config['mothers'] if any(p['id']==m['proxy_id'] and p['enabled'] for p in config['proxies']))
 config['mothers'].remove(selected);config['mothers'].insert(0,selected)
 mid=selected['id'];pid=config['proxies'][0]['id']
 def row_action(label,action):
  batch(['wait','500'])
  evaluate('(()=>{const row=[...document.querySelectorAll("tr")].find(r=>r.textContent.includes('+json.dumps(label)+'));[...row.querySelectorAll("button")].find(b=>b.textContent.trim()==='+json.dumps(action)+').click()})()')
  batch(['wait','250'])
 row_action(config['mothers'][0]['name'],'校验 / 刷新席位')
 expect('普通席位：3 / 8');expect('临停 2');expect('高级席位：0 / 0')
 row_action(config['proxies'][0]['name'],'测试代理');expect('出口 IP：203.0.113.7')
 click('读取状态');expect('普通席位：3 / 8');expect('出口 IP：203.0.113.7')
 current=evaluate('fetch("/api/admin/refund-settings").then(r=>r.json())')
 assert next(m for m in current['mothers'] if m['id']==mid)['check']['seats']['standard']['total']==8
 assert next(p for p in current['proxies'] if p['id']==pid)['check']['valid']
 assert 'private-protocol-canary' not in json.dumps(current)
 row_action(config['mothers'][0]['name'],'编辑')
 assert evaluate('document.querySelectorAll("[role=dialog]").length')==1
 evaluate('([...document.querySelectorAll("[role=dialog] button")].find(b=>b.textContent.trim()==="校验 / 刷新席位")).click()')
 batch(['wait','600']);expect('母号凭据和团队访问校验通过')
 assert evaluate('document.querySelector("[role=dialog] textarea")?.value === ""')
 batch(['wait','500'])
 click('取消')
 row_action(config['proxies'][0]['name'],'编辑')
 evaluate('([...document.querySelectorAll("[role=dialog] button")].find(b=>b.textContent.trim()==="测试代理")).click()')
 batch(['wait','600']);expect('出口 IP：203.0.113.7');click('取消')
 shot('resource-check-desktop')
 batch(['set','viewport','390','844']);shot('resource-check-mobile')
 assert not evaluate('document.documentElement.scrollWidth > innerWidth'),'mobile overflow'
 batch(['set','viewport','1540','1040'])
 click('资金与账单');expect('每 1 积分对应 1 R')
 click('使用指南');batch(['click','.help-card details:first-child summary']);expect('每 1 积分对应 1 R')
 assert '人民币' not in text()
 for _ in range(30):
  if not capture.pending:break
  time.sleep(.1)
 assert not capture.errors,capture.errors
 assert len(capture.bodies)>=6,'insufficient capture'
 assert all(url.startswith(BASE) for url in capture.urls if url.startswith(('http:','https:'))),'browser contacted upstream'
 for url,body in capture.bodies:
  assert not any(word in body for word in PRIVATE+('private-protocol-canary',)),'private content in response'
 assert any('/refund-mothers/check' in url for url,_ in capture.bodies)
 assert any('/refund-proxies/check' in url for url,_ in capture.bodies)
 assert any('/assets/admin-execution-' in url for url,_ in capture.bodies)
 print('Verified: persistent seats/proxy checks, modal checks, credential privacy, desktop/mobile, R wording; audited '+str(len(capture.bodies))+' response bodies',flush=True)
finally:
 if capture: capture.close()
 subprocess.run(COMMAND+['--session',SESSION,'close'],capture_output=True,timeout=20)

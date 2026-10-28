"""Multi-account report exclusion acceptance against isolated QA only."""
from pathlib import Path

helper=Path(__file__).resolve().parent/'browser-smoke.py'
exec(compile(helper.read_text(encoding='utf-8').split('\ntry:\n',1)[0],str(helper),'exec'))
SESSION='huixu-report-exclusions-qa'
ARTIFACTS=ROOT/'.local/report-exclusions-qa';ARTIFACTS.mkdir(exist_ok=True)

def request(path,method='GET',body=None):
    options={'method':method,'headers':{'Content-Type':'application/json','Idempotency-Key':'qa-report-'+str(time.time_ns())}}
    if body is not None:options['body']=json.dumps(body)
    result=evaluate('fetch('+json.dumps(path)+','+json.dumps(options)+').then(async r=>({status:r.status,data:await r.json()}))')
    assert result['status']==200,result['status']
    return result['data']

def wait_report(total):
    for _ in range(30):
        if evaluate('!document.querySelector(".report-loading") && !!document.querySelector(".report-caption")') and str(total)+' 个账户' in text():return
        time.sleep(.15)
    raise AssertionError('Report did not update after selection')

def select_email(email):
    evaluate('(()=>{const label=[...document.querySelectorAll(".exclusion-options label")].find(e=>e.textContent.includes('+json.dumps(email)+'));if(!label)throw Error("option missing");label.querySelector("input").click()})()')

try:
    subprocess.run(COMMAND+['--session',SESSION,'open',BASE],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=45,check=True)
    batch(['open',BASE],['set','viewport','1600','1050'])
    fill('登录邮箱',CONFIG['BOOTSTRAP_EMAIL']);fill('账户密码',CONFIG['BOOTSTRAP_PASSWORD']);click('进入服务台');expect('Admin，欢迎回来。')
    assert request('/api/admin/refund-settings')['settings']['enabled'] is False
    suffix=str(time.time_ns());people=[]
    for i,amount in enumerate([1100,2200]):
        email='report-exclusion-'+str(i)+'-'+suffix+'@example.test'
        user=request('/api/admin/users','POST',{'email':email,'name':'排除验收 '+str(i+1),'password':'Isolated-Report-2026-'+suffix,'role':'member','service_fee':3000})
        request('/api/admin/users/'+user['id']+'/credit','POST',{'amount':amount,'note':'isolated report QA'})
        people.append({**user,'email':email})
    baseline=request('/api/admin/reports/accounts?size=1')
    click('运营控制台');click('账户报表','tab');wait_report(baseline['total'])
    evaluate('document.querySelector(".account-exclusions summary").click()')
    fill('搜索可排除账号','report-exclusion-','searchbox');batch(['wait','600'])
    for index,person in enumerate(people):
        select_email(person['email']);wait_report(baseline['total']-index-1)
    assert evaluate('document.querySelectorAll(".excluded-tags button").length')==2
    filtered=request('/api/admin/reports/accounts?size=1'+''.join('&exclude_user_id='+p['id'] for p in people))
    assert filtered['summary']['recharged']==baseline['summary']['recharged']-3300
    assert filtered['summary']['balance']==baseline['summary']['balance']-3300
    assert evaluate('document.querySelector(".report-metrics section strong").textContent')==str(filtered['summary']['recharged']/100).removesuffix('.0')
    shot('desktop-selected')
    evaluate('document.querySelector(".account-exclusions").open=false')
    click('每月');wait_report(baseline['total']-2)
    assert '额外排除 2 个账号' in text()
    batch(['uncheck',ref('排除管理员账号','checkbox')]);batch(['wait','500'])
    assert '额外排除 2 个账号' in text()
    batch(['check',ref('排除管理员账号','checkbox')]);click('每日');wait_report(baseline['total']-2)
    batch(['set','viewport','390','844'])
    evaluate('document.querySelector(".account-exclusions summary").click()');batch(['wait','350'])
    assert not evaluate('document.documentElement.scrollWidth>innerWidth')
    assert evaluate('(()=>{const r=document.querySelector(".exclusion-panel").getBoundingClientRect();return r.left>=0&&r.right<=innerWidth})()')
    shot('mobile-selected')
    click('清空');wait_report(baseline['total'])
    assert '额外排除 2 个账号' not in text()
    batch(['set','viewport','1600','1050']);shot('desktop-cleared')
    assert request('/api/admin/refund-settings')['settings']['enabled'] is False
    result={'passed':True,'multi_select':True,'search':True,'summary_and_rows':True,'period_keeps_selection':True,'admin_filter_composes':True,'clear':True,'mobile_overflow':False,'scope':'isolated QA only'}
    (ARTIFACTS/'result.json').write_text(json.dumps(result,indent=2),encoding='utf-8')
    print(json.dumps(result),flush=True)
finally:
    try:batch(['close'])
    except Exception:pass

"""Monitor interaction QA, isolated 18743 only; no real upstream work."""
from pathlib import Path

helpers = Path(__file__).with_name('browser-smoke.py')
scope = {'__file__': str(helpers)}
exec(compile(helpers.read_text(encoding='utf-8').split('\ntry:\n', 1)[0], str(helpers), 'exec'), scope)
globals().update(scope)
SESSION = 'huixu-monitor-qa'
scope['SESSION'] = SESSION
ARTIFACTS = ROOT / '.local' / 'monitor-qa'
ARTIFACTS.mkdir(exist_ok=True)
scope['ARTIFACTS'] = ARTIFACTS
fixture = json.loads((ROOT / '.local/monitor-fixture.json').read_text(encoding='utf-8'))

def row_count(n):
    batch(['wait','--fn',f'document.querySelectorAll(".monitor-list tbody tr .monitor-id").length==={n}'])

def select(label, value):
    batch(['select',ref(label,'combobox'),value],['wait','250'])

try:
    subprocess.run(COMMAND+['--session',SESSION,'open',BASE],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=45,check=True)
    batch(['open',BASE],['set','viewport','1600','1080'])
    fill('登录邮箱',CONFIG['BOOTSTRAP_EMAIL']);fill('账户密码',CONFIG['BOOTSTRAP_PASSWORD']);click('进入服务台')
    expect('Admin，欢迎回来。');click('运营控制台');click('申请全监控','tab');expect('跟踪所有客户的申请')
    fill('搜索监控申请',fixture['email'],'searchbox');row_count(8)
    assert evaluate('[...document.querySelectorAll(".monitor-metric strong")].map(e=>+e.textContent)')==[1,2,1,1,2,1]
    assert evaluate('document.querySelector(".monitor-list tbody tr").textContent.includes("待跟进")')
    assert evaluate('document.querySelector("select[aria-label=每页条数]").value')=='100'
    assert all(x not in text() for x in ('opaque-protocol-canary','export_and_deactivate','private-child-password'))
    shot('monitor-desktop')
    evaluate('document.querySelectorAll(".monitor-metric")[4].click()');row_count(2)
    assert '待跟进' not in evaluate('document.querySelector(".monitor-list tbody").textContent')
    select('监控办理状态','running');row_count(2)
    select('监控办理状态','');select('监控费用状态','released');row_count(3)
    select('监控费用状态','');select('监控关注项','long_wait');row_count(1)
    select('监控关注项','long_running');row_count(2);select('监控关注项','');row_count(8)
    select('监控排序','newest');row_count(8)
    fill('搜索监控申请','absent-monitor@example.test','searchbox');row_count(0);expect('当前筛选条件下没有申请')
    fill('搜索监控申请',fixture['email'],'searchbox');row_count(8)
    # Time filter controls; both dates are interpreted by the backend in Beijing.
    batch(['fill','[aria-label=申请开始日期]',fixture['date']],['fill','[aria-label=申请结束日期]',fixture['date']])
    row_count(8)
    attention=next(r for r in fixture['rows'] if r['status']=='attention')
    click('监控详情 '+attention['email']);expect('服务单详情')
    assert not any(x in text() for x in ('opaque-protocol-canary','export_and_deactivate'))
    click('关闭');row_count(8)
    # Observe real fetches rather than simulating responses.
    evaluate('window.monitorCalls=0;window.monitorFetch=window.fetch;window.fetch=(...args)=>{if(String(args[0]).includes("/api/admin/refund-monitor"))window.monitorCalls++;return window.monitorFetch(...args)}')
    select('监控刷新间隔','5')
    batch(['wait','--fn','window.monitorCalls>0'])
    batch(['uncheck',ref('自动刷新','checkbox')])
    before=evaluate('window.monitorCalls');batch(['wait','5400']);assert evaluate('window.monitorCalls')==before
    click('立即刷新');assert evaluate('window.monitorCalls')>before
    batch(['check',ref('自动刷新','checkbox')]);click('监控详情 '+attention['email']);expect('服务单详情')
    before=evaluate('window.monitorCalls');batch(['wait','5400']);assert evaluate('window.monitorCalls')==before
    click('关闭');row_count(8)
    batch(['set','viewport','390','844']);shot('monitor-mobile')
    assert not evaluate('document.documentElement.scrollWidth>innerWidth')
    # Export is the displayed page, with only business columns.
    assert evaluate('document.querySelector(".monitor-list table").scrollWidth>document.querySelector(".table-scroll").clientWidth')
    click('账户管理','tab');before=evaluate('window.monitorCalls');batch(['wait','5400']);assert evaluate('window.monitorCalls')==before
    assert not batch(['errors'])[0].get('errors'), 'browser runtime errors'
    print('PASS: monitor counts, business privacy, priority, filters, dates, empty state, detail, auto/manual refresh, modal pause, unmount cleanup, desktop/mobile.',flush=True)
finally:
    batch(['close'])

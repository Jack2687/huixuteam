<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { api, money, date as formatTime } from '../lib/api';
import Icon from './Icon.vue';
import Pagination from './Pagination.vue';
import ReportAccountExclusions from './ReportAccountExclusions.vue';
type Metrics = { recharged:number; debited:number; freeze_added:number; released:number; used:number; settled_count:number; balance:number; frozen:number; available:number; applications:number; queued:number; processing:number; succeeded:number; attention:number; failed:number; cancelled:number };
type Row = Metrics & { id:string; name:string; email:string; role:string; disabled:boolean; service_fee:number };
type Report = { items:Row[]; summary:Metrics; total:number; page:number; size:number; start:string; end:string; generated_at:string };
type Period = 'day'|'week'|'month'|'year';
const periods:{key:Period;label:string}[] = [{key:'day',label:'每日'},{key:'week',label:'每周'},{key:'month',label:'每月'},{key:'year',label:'每年'}];
const today = () => new Date(Date.now()+8*3600000).toISOString().slice(0,10);
const period=ref<Period>('day'), anchor=ref(today()), excludeAdmin=ref(true), search=ref(''), query=ref('');
const page=ref(1), size=ref(100), loading=ref(false), error=ref(''), report=ref<Report>();
const excludedAccounts=ref<{id:string;name:string;email:string}[]>([]);
const range=computed(()=>report.value ? (report.value.start===report.value.end ? report.value.start : `${report.value.start} 至 ${report.value.end}`) : '');
let controller:AbortController|undefined;
async function load(){
 controller?.abort();const current=new AbortController();controller=current;
 loading.value=true;error.value='';report.value=undefined;
 try {
  if(!anchor.value)throw Error('请选择统计日期');
  const params=new URLSearchParams({period:period.value,date:anchor.value,q:query.value,exclude_admin:String(excludeAdmin.value),page:String(page.value),size:String(size.value)});
  for(const account of excludedAccounts.value)params.append('exclude_user_id',account.id);
  const data=await api<Report>(`/api/admin/reports/accounts?${params}`,{signal:current.signal});
  if(controller===current)report.value=data;
 } catch(e){if(controller===current&&(e as Error).name!=='AbortError')error.value=(e as Error).message}
 finally{if(controller===current)loading.value=false}
}
watch([period,anchor,excludeAdmin,excludedAccounts,query,size],()=>{page.value=1},{flush:'sync'});
watch([period,anchor,excludeAdmin,excludedAccounts,query,page,size],load,{immediate:true});
onUnmounted(()=>controller?.abort());
function shift(direction:number){
 const d=new Date(`${report.value?.start||anchor.value}T00:00:00Z`);
 if(period.value==='year')d.setUTCFullYear(d.getUTCFullYear()+direction);
 else if(period.value==='month'){d.setUTCDate(1);d.setUTCMonth(d.getUTCMonth()+direction)}
 else d.setUTCDate(d.getUTCDate()+direction*(period.value==='week'?7:1));
 anchor.value=d.toISOString().slice(0,10);
}
const roles:Record<string,string>={admin:'管理员',operator:'运营人员',member:'普通用户'};
</script>
<template>
 <section class="account-reports" :aria-busy="loading">
  <div class="section-title"><div><h2>账户报表</h2><p>按人核对资金与服务申请 · 金额单位为积分，1 积分 = 1 R</p></div><button class="button secondary" :disabled="loading" @click="load"><Icon name="RefreshCw" :size="15"/>刷新报表</button></div>
  <div class="report-controls">
   <div class="report-periods" role="group" aria-label="统计周期"><button v-for="p in periods" :key="p.key" :aria-pressed="period===p.key" @click="period=p.key">{{p.label}}</button></div>
   <label class="report-date">统计日期<input v-model="anchor" aria-label="统计日期" type="date" min="1900-01-01" max="9998-12-31"/></label>
   <div class="report-navigation"><button class="button secondary" :disabled="loading||!report" @click="shift(-1)">上一期</button><button class="button secondary" @click="anchor===today()?load():anchor=today()">本期</button><button class="button secondary" :disabled="loading||!report" @click="shift(1)">下一期</button></div>
   <label class="report-exclude"><input v-model="excludeAdmin" type="checkbox"/>排除管理员账号</label>
   <ReportAccountExclusions v-model="excludedAccounts" :exclude-admin="excludeAdmin"/>
   <form class="report-search" @submit.prevent="query=search.trim()"><input v-model="search" aria-label="搜索报表账户" placeholder="搜索姓名或邮箱" maxlength="254"/><button class="button secondary" type="submit">查询</button></form>
  </div>
  <div v-if="error" class="error-banner" role="alert">{{error}}<button @click="load">重试</button></div>
  <p v-if="loading" class="report-loading" role="status">正在汇总账户数据…</p>
  <template v-if="report">
   <div class="report-caption"><strong>{{range}}</strong><span>北京时间 · 周一为每周起点 · {{report.total}} 个账户{{excludeAdmin?'（已排除管理员）':''}}{{excludedAccounts.length?` · 额外排除 ${excludedAccounts.length} 个账号`:''}}</span></div>
   <div class="report-metrics">
    <section><span>期间充值</span><strong>{{money(report.summary.recharged)}}</strong><small>人工入账 + 兑换入账 · 人工扣减 {{money(report.summary.debited)}}</small></section>
    <section><span>当前冻结</span><strong>{{money(report.summary.frozen)}}</strong><small>期间新增 {{money(report.summary.freeze_added)}} · 释放 {{money(report.summary.released)}}</small></section>
    <section><span>期间已使用</span><strong>{{money(report.summary.used)}}</strong><small>实际结算 {{report.summary.settled_count}} 笔 · 同时解除对应冻结</small></section>
    <section><span>期间提交申请</span><strong>{{report.summary.applications}}<em> 笔</em></strong><small>已完成 {{report.summary.succeeded}} · 待办理 {{report.summary.queued}} · 办理中 {{report.summary.processing}}<br/>需跟进 {{report.summary.attention}} · 未成功 {{report.summary.failed}} · 已撤回 {{report.summary.cancelled}}</small></section>
   </div>
   <div class="table-card">
    <div class="table-scroll"><table class="report-table"><thead><tr><th>账户</th><th>期间充值 / 扣减</th><th>冻结情况</th><th>期间已使用</th><th>服务单价</th><th>当前余额</th><th>期间申请 · 当前进展</th></tr></thead>
     <tbody><tr v-for="row in report.items" :key="row.id">
      <td><strong>{{row.name}}</strong><small>{{row.email}}</small><span class="badge" :class="row.disabled?'gray':'blue'">{{row.disabled?'已停用':roles[row.role]}}</span></td>
      <td><strong class="report-positive">+{{money(row.recharged)}}</strong><small>人工扣减 {{money(row.debited)}}</small></td>
      <td><strong>{{money(row.frozen)}} <span class="report-hint">当前</span></strong><small>期间新增 {{money(row.freeze_added)}}<br/>期间释放 {{money(row.released)}}</small></td>
      <td><strong>{{money(row.used)}}</strong><small>结算 {{row.settled_count}} 笔</small></td>
      <td><strong>{{money(row.service_fee)}} <span class="report-hint">/ 笔</span></strong><small>当前配置<br/>期间平均实收 {{row.settled_count?money(row.used/row.settled_count):'—'}}</small></td>
      <td><strong>{{money(row.available)}} <span class="report-hint">可用</span></strong><small>总额 {{money(row.balance)}}<br/>含当前冻结</small></td>
      <td><strong>{{row.applications}} 笔</strong><div class="report-statuses"><span>完成 <b>{{row.succeeded}}</b></span><span>待办 <b>{{row.queued}}</b></span><span>办理中 <b>{{row.processing}}</b></span><span>需跟进 <b>{{row.attention}}</b></span><span>未成功 <b>{{row.failed}}</b></span><span>撤回 <b>{{row.cancelled}}</b></span></div></td>
     </tr><tr v-if="!report.items.length"><td colspan="7" class="report-empty">没有符合条件的账户</td></tr></tbody>
    </table></div>
    <Pagination v-model:page="page" v-model:size="size" :total="report.total" label="账户报表分页"/>
   </div>
   <div class="report-notes"><p>充值、冻结变动和使用按所选期间的实际账单统计；人工扣减不算服务消费。单价调整不影响历史结算。</p><p>余额、当前冻结和配置单价为查询时的最新值。申请数量按期间提交时间统计，进展为当前状态；“未成功”包含受限申请。历史申请本期完成的费用计入本期已使用。</p><p>顶部合计覆盖全部符合筛选条件的账户。数据更新于 {{formatTime(report.generated_at)}}。</p></div>
  </template>
 </section>
</template>
<style scoped>
.account-reports{min-width:0}.report-controls{display:flex;align-items:center;flex-wrap:wrap;gap:12px;padding:16px;background:#fff;border:1px solid #e4e8e5;border-radius:10px;margin-bottom:20px}.report-periods{display:flex;background:#f1f4f1;padding:3px;border-radius:7px}.report-periods button{padding:8px 14px;font-size:13px;border-radius:5px;color:#66736a}.report-periods button[aria-pressed=true]{background:#244b3c;color:white}.report-date,.report-exclude{display:flex;align-items:center;gap:8px;font-size:12px;white-space:nowrap}.report-date input,.report-search input{padding:8px 10px;border:1px solid #dce3dd;border-radius:5px;min-width:0}.report-navigation{display:flex;gap:6px}.report-search{display:flex;gap:6px;margin-left:auto}.report-search input{width:190px}.report-caption{display:flex;align-items:baseline;flex-wrap:wrap;gap:12px;margin-bottom:12px}.report-caption strong{font-size:16px}.report-caption span{font-size:12px;color:#748078}.report-metrics{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:14px;margin-bottom:20px}.report-metrics section{padding:20px;border:1px solid #dce4dd;border-radius:10px;background:linear-gradient(140deg,#f8fbf7,#fff);min-width:0}.report-metrics span{font-size:12px;color:#67786a}.report-metrics strong{display:block;font-size:30px;font-weight:650;color:#284c3a;margin:12px 0;font-variant-numeric:tabular-nums;overflow-wrap:anywhere}.report-metrics em{font-size:13px;font-style:normal}.report-metrics small{font-size:11px;color:#6d7970;line-height:1.8}.report-table{min-width:920px;font-variant-numeric:tabular-nums}.report-table th,.report-table td{padding:14px 10px}.report-table td{vertical-align:top}.report-table td:first-child,.report-table th:first-child{padding-left:16px}.report-table td:last-child,.report-table th:last-child{padding-right:16px}.report-table td:first-child{max-width:250px;overflow-wrap:anywhere;white-space:normal}.report-table strong{font-size:13px;font-weight:600}.report-table small{display:block;font-size:11px;line-height:1.8;margin:4px 0;color:#778179}.report-positive{color:#257555}.report-hint{font-size:11px;font-weight:400;color:#859086}.report-statuses{display:grid;grid-template-columns:repeat(3,1fr);gap:5px 12px;margin-top:6px;font-size:11px;color:#748078}.report-statuses b{color:#46584a;font-weight:500}.report-notes{font-size:12px;color:#7a837c;line-height:1.8;margin-top:16px}.report-notes p{margin:4px 0}.report-loading,.report-empty{text-align:center;padding:40px;color:#879188}
@media(max-width:1100px){.report-metrics{grid-template-columns:repeat(2,minmax(0,1fr))}}
@media(max-width:600px){.report-controls{padding:12px;gap:12px}.report-search{width:100%;margin:0}.report-search input{flex:1;width:0}.report-periods{width:100%}.report-periods button{flex:1}.report-metrics{gap:8px}.report-metrics section{padding:13px}.report-metrics strong{font-size:23px}.report-caption{gap:4px}.report-controls .button{padding:7px 10px}.report-date{flex-wrap:wrap}}
</style>

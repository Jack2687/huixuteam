<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, onUnmounted, ref, watch } from 'vue';
import { api, money, statuses, feeLabel, exportCSV, type Refund } from '../lib/api';
import Icon from './Icon.vue';
import Pagination from './Pagination.vue';
import RefundDetail from './RefundDetail.vue';
const AdminRefundTimeline=defineAsyncComponent(()=>import('./AdminRefundTimeline.vue'));
defineProps<{canTimeline:boolean}>();

type Row = Refund & {recovery_alert:boolean;recovery_pending:boolean;next_check_at:string|null;user_id:string;user_name:string;user_email:string;started_at:string|null;finished_at:string|null;wait_seconds:number|null;handle_seconds:number|null;long_wait:boolean;long_running:boolean};
type Summary = {recovery_alert:number;recovery_pending:number;total:number;queued:number;running:number;attention:number;succeeded:number;failed:number;cancelled:number;long_wait:number;long_running:number;frozen:number;settled:number;released:number};
type Monitor = {items:Row[];summary:Summary;total:number;page:number;size:number;generated_at:string};
const emit=defineEmits<{toast:[message:string];changed:[]}>();
const data=ref<Monitor>(),loading=ref(false),error=ref(''),detail=ref('');
const query=ref(''),search=ref(''),status=ref(''),fee=ref(''),focus=ref(''),sort=ref('priority'),start=ref(''),end=ref(''),page=ref(1),size=ref(100);
const automatic=ref(true),refreshSeconds=ref(10);
const cards=[{key:'queued',label:'排队中',filter:'queued',tone:'amber'}, {key:'running',label:'正在处理',filter:'running',tone:'blue'}, {key:'attention',label:'待跟进',filter:'attention',tone:'orange'}, {key:'succeeded',label:'已完成',filter:'succeeded',tone:'green'}, {key:'failed',label:'未完成 / 已封号',filter:'unsuccessful',tone:'red'}, {key:'cancelled',label:'已取消',filter:'cancelled',tone:'gray'}] as const;
const summary=computed(()=>data.value?.summary);
let controller:AbortController|undefined,sequence=0,timer:ReturnType<typeof setInterval>,debounce:ReturnType<typeof setTimeout>;
const timestamp=(value:string|null|undefined)=>value?new Intl.DateTimeFormat('zh-CN',{timeZone:'Asia/Shanghai',year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',second:'2-digit',hour12:false}).format(new Date(value)):'—';
function duration(seconds:number|null){
 if(seconds===null)return '—';
 const n=Math.max(0,Math.floor(seconds));
 if(n<60)return `${n} 秒`;
 if(n<3600)return `${Math.floor(n/60)} 分 ${n%60} 秒`;
 if(n<86400)return `${Math.floor(n/3600)} 小时 ${Math.floor(n%3600/60)} 分`;
 return `${Math.floor(n/86400)} 天 ${Math.floor(n%86400/3600)} 小时`;
}
async function load(background=false){
 if(background&&(document.hidden||detail.value||loading.value))return;
 controller?.abort();controller=new AbortController();const current=++sequence;
 loading.value=true;error.value='';
 try{
  const params=new URLSearchParams({q:query.value,status:status.value,fee_status:fee.value,focus:focus.value,sort:sort.value,start:start.value,end:end.value,page:String(page.value),size:String(size.value)});
  const result=await api<Monitor>(`/api/admin/refund-monitor?${params}`,{signal:controller.signal});
  if(current!==sequence)return;
  data.value=result;
  const pages=Math.max(1,Math.ceil(result.total/size.value));if(page.value>pages)page.value=pages;
 }catch(e){if(current===sequence&&(e as Error).name!=='AbortError')error.value=(e as Error).message}
 finally{if(current===sequence)loading.value=false}
}
function setupTimer(){clearInterval(timer);if(automatic.value)timer=setInterval(()=>load(true),refreshSeconds.value*1000)}
function onVisible(){if(automatic.value)load(true)}
function reset(){search.value='';query.value='';status.value='';fee.value='';focus.value='';start.value='';end.value='';sort.value='priority';page.value=1}
function chooseStatus(value:string){status.value=value;focus.value=''}
function exportPage(){
 if(!data.value)return;
 exportCSV([['申请编号','服务账号','客户称呼','客户邮箱','办理状态','服务费（分）','结算状态','提交时间','开始时间','结束时间','排队秒数','办理历时秒数','当前说明'],...data.value.items.map(r=>[r.id,r.email,r.user_name,r.user_email,statuses[r.status]?.label||r.status,String(r.fee/100),feeLabel[r.fee_status],timestamp(r.created_at),timestamp(r.started_at),timestamp(r.finished_at),r.wait_seconds===null?'':String(r.wait_seconds),r.handle_seconds===null?'':String(r.handle_seconds),r.message])],'回序-申请全监控.csv');
 emit('toast','当前页监控记录已导出');
}
watch(search,()=>{clearTimeout(debounce);debounce=setTimeout(()=>{query.value=search.value.trim()},300)});
watch([query,status,fee,focus,start,end,sort,size],()=>{page.value=1;load()});
watch(page,()=>load());watch([automatic,refreshSeconds],setupTimer);
onMounted(()=>{load();setupTimer();document.addEventListener('visibilitychange',onVisible)});
onUnmounted(()=>{sequence++;controller?.abort();clearInterval(timer);clearTimeout(debounce);document.removeEventListener('visibilitychange',onVisible)});
</script>

<template>
 <div class="refund-monitor">
  <header class="monitor-heading">
   <div><p class="eyebrow">APPLICATION MONITOR</p><h2>申请全监控</h2><p class="muted">跟踪所有客户的申请、处理时长与费用状态。</p></div>
   <div class="monitor-refresh"><label class="checkbox-label"><input v-model="automatic" type="checkbox"/>自动刷新</label><select v-model.number="refreshSeconds" aria-label="监控刷新间隔"><option v-for="n in [5,10,30,60]" :key="n" :value="n">{{n}} 秒</option></select><button class="button secondary" :disabled="loading" @click="load()"><Icon name="RefreshCw" :class="{spin:loading}"/>立即刷新</button></div>
  </header>
  <div v-if="error" class="error-banner" role="alert">{{error}}<span v-if="data"> · 当前保留上次读取的结果</span></div>
  <div v-if="summary?.recovery_alert" class="error-banner recovery-alert" role="alert"><strong>{{summary.recovery_alert}} 笔申请异常占用超时</strong><span>请核实处理结果或清理进度，相关资源仍保留。</span><button class="text-button" @click="status='';focus='recovery_alert'">立即跟进</button></div>
  <div class="monitor-metrics">
   <button v-for="card in cards" :key="card.key" class="monitor-metric" :class="[card.tone,{selected:status===card.filter&&!focus}]" @click="chooseStatus(card.filter)"><span>{{card.label}}</span><strong>{{summary?.[card.key]??'—'}}</strong><small>查看申请 <Icon name="ArrowUpRight" :size="13"/></small></button>
  </div>
  <div class="monitor-totals"><span>恢复检查 <b>{{summary?.recovery_pending??0}}</b> 笔</span><span>匹配申请 <b>{{summary?.total??0}}</b> 笔</span><span>冻结服务费 <b>{{money(summary?.frozen)}} 分</b></span><span>已结算 <b>{{money(summary?.settled)}} 分</b></span><span>已释放 <b>{{money(summary?.released)}} 分</b></span></div>
  <section class="table-card monitor-list">
   <div class="monitor-filters">
    <input v-model="search" type="search" aria-label="搜索监控申请" placeholder="搜索服务账号、客户称呼 / 邮箱、申请编号…"/>
    <label>申请日期（北京时间）<div class="monitor-dates"><input v-model="start" type="date" aria-label="申请开始日期"/><span>至</span><input v-model="end" type="date" aria-label="申请结束日期"/></div></label>
    <div class="monitor-selects">
     <select v-model="status" aria-label="监控办理状态"><option value="">全部办理状态</option><option value="running">正在处理（含核验）</option><option value="unsuccessful">未完成 / 已封号</option><option value="abnormal">异常（待跟进 / 未完成 / 已封号）</option><option v-for="(item,key) in statuses" :key="key" :value="key">{{item.label}}</option></select>
     <select v-model="fee" aria-label="监控费用状态"><option value="">全部费用状态</option><option v-for="(label,key) in feeLabel" :key="key" :value="key">{{label}}</option></select>
     <select v-model="focus" aria-label="监控关注项"><option value="">全部关注项</option><option value="recovery_alert">异常占用超时</option><option value="long_wait">排队超过 10 分钟</option><option value="long_running">处理超过 15 分钟</option></select>
     <select v-model="sort" aria-label="监控排序"><option value="priority">优先跟进与在途申请</option><option value="newest">提交时间从新到旧</option><option value="oldest">提交时间从旧到新</option><option value="updated">最近状态更新</option></select>
     <button class="text-button" @click="reset">重置筛选</button>
    </div>
   </div>
   <div class="monitor-statusline"><span><i class="monitor-dot" :class="{live:automatic&&!error}"/>{{loading?'正在读取…':automatic?'自动刷新已开启':'自动刷新已暂停'}}<small v-if="data"> · {{timestamp(data.generated_at)}}</small></span><button class="text-button" :disabled="!data?.items.length||loading" @click="exportPage"><Icon name="Download" :size="14"/>导出当前页</button></div>
   <div class="monitor-attention"><button @click="status='';focus='long_wait'">排队超 10 分钟 <b>{{summary?.long_wait??0}}</b></button><button @click="status='';focus='long_running'">处理超 15 分钟 <b>{{summary?.long_running??0}}</b></button><span>仅提示关注，不自动判定失败。顶部汇总按搜索和日期统计；下表应用全部筛选。</span></div>
   <div class="table-scroll" :aria-busy="loading"><table><thead><tr><th>申请 / 服务账号</th><th>所属客户</th><th>状态与进展</th><th>服务费 / 结算</th><th>时间（北京时间）</th><th>等待与办理历时</th><th>操作</th></tr></thead><tbody>
    <tr v-for="r in data?.items" :key="r.id" :class="{'monitor-alert':r.status==='attention'||r.long_running||r.recovery_alert}">
     <td><strong>{{r.email}}</strong><small class="monitor-id" :title="r.id">#{{r.id}}</small></td>
     <td><strong>{{r.user_name}}</strong><small>{{r.user_email}}</small></td>
     <td><span class="badge" :class="statuses[r.status]?.color">{{statuses[r.status]?.label}}</span><p class="monitor-message">{{r.message}}</p><small v-if="r.recovery_alert" class="duration-alert">异常占用超时，请跟进</small><small v-else-if="r.recovery_pending">已安排自动恢复检查</small><small v-if="r.next_check_at">下次 {{timestamp(r.next_check_at)}}</small></td>
     <td><strong>{{money(r.fee)}} 分</strong><small :class="{'credit-text':r.fee_status==='settled'}">{{feeLabel[r.fee_status]}}</small></td>
     <td class="monitor-times"><span>提交 {{timestamp(r.created_at)}}</span><span>开始 {{timestamp(r.started_at)}}</span><span>结束 {{timestamp(r.finished_at)}}</span></td>
     <td><span :class="{'duration-alert':r.long_wait}">排队 {{duration(r.wait_seconds)}}</span><small :class="{'duration-alert':r.long_running}">办理 {{duration(r.handle_seconds)}}</small><small class="muted">更新 {{timestamp(r.updated_at)}}</small></td>
     <td><button class="text-button" :aria-label="`监控详情 ${r.email}`" @click="detail=r.id">{{r.status==='attention'?'查看并跟进':'查看详情'}}<Icon name="ChevronRight" :size="13"/></button></td>
    </tr>
    <tr v-if="!data?.items.length"><td colspan="7" class="monitor-empty">{{loading?'正在读取申请…':'当前筛选条件下没有申请'}}</td></tr>
   </tbody></table></div>
   <Pagination v-model:page="page" v-model:size="size" :total="data?.total??0" label="申请全监控分页"/>
   <p class="monitor-note">办理历时包含待跟进期间；缺少历史开始记录时显示“—”。打开详情期间暂停列表自动刷新。</p>
  </section>
  <RefundDetail v-if="detail" :key="detail" :id="detail" admin @close="detail='';load()" @changed="load();emit('changed')"><template #monitor="{status}"><AdminRefundTimeline v-if="canTimeline" :id="detail" :status="status"/></template></RefundDetail>
 </div>
</template>

<style scoped>
.recovery-alert{display:flex;gap:14px;align-items:center;flex-wrap:wrap}.recovery-alert strong{font-size:16px}

.refund-monitor{display:grid;gap:18px;min-width:0}.monitor-heading{display:flex;align-items:center;justify-content:space-between;gap:20px}.monitor-heading h2{font-size:24px;margin:7px 0}.monitor-heading .muted{font-size:12px}.monitor-refresh{display:flex;align-items:center;gap:10px;flex-wrap:wrap}.monitor-refresh select{font-size:12px}.monitor-metrics{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px}.monitor-metric{text-align:left;background:#fff;border:1px solid var(--line);border-radius:8px;padding:18px;display:grid;gap:10px;cursor:pointer;color:var(--text)}.monitor-metric.selected{border-color:#ab633c;box-shadow:inset 0 0 0 1px #ab633c}.monitor-metric>span{font-size:12px;color:#687666}.monitor-metric strong{font-size:30px;font-weight:650}.monitor-metric small{font-size:10px;color:#85917b;display:flex;align-items:center;gap:5px}.monitor-metric.amber strong{color:#aa7426}.monitor-metric.blue strong{color:#436f8a}.monitor-metric.orange strong,.monitor-metric.red strong{color:#ad4a31}.monitor-metric.green strong{color:#4e7657}.monitor-totals{display:flex;gap:20px;flex-wrap:wrap;font-size:12px;background:#edeede;padding:14px 18px;border-radius:7px}.monitor-totals b{margin:0 3px}.monitor-list{margin:0;min-width:0}.monitor-filters{padding:20px;display:flex;flex-wrap:wrap;align-items:end;gap:15px}.monitor-filters>input{flex:1;min-width:240px;font-size:12px}.monitor-filters label{display:grid;gap:7px;font-size:11px;color:#718064}.monitor-dates{display:flex;align-items:center;gap:8px}.monitor-dates input{font-size:12px;min-width:0}.monitor-selects{display:flex;gap:10px;flex-wrap:wrap;width:100%}.monitor-selects select{font-size:11px;flex:1;min-width:145px}.monitor-statusline{display:flex;justify-content:space-between;align-items:center;gap:10px;padding:12px 20px;border-top:1px solid var(--line);font-size:11px;color:#76856c}.monitor-dot{display:inline-block;width:7px;height:7px;border-radius:50%;background:#aaa;margin-right:7px}.monitor-dot.live{background:#618563}.monitor-attention{padding:0 20px 14px;display:flex;gap:12px;align-items:center;flex-wrap:wrap}.monitor-attention button{font-size:11px;background:#f6eddc;border:1px solid #e3d9bf;border-radius:4px;padding:5px 8px;color:#8d672a;cursor:pointer}.monitor-attention>span{font-size:10px;color:#869078}.monitor-list table{min-width:1220px}.monitor-list td{vertical-align:top;font-size:12px}.monitor-list td strong{font-size:12px;font-weight:550}.monitor-list td small{display:block;font-size:10px;margin-top:7px;color:#849077}.monitor-id{max-width:205px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-family:monospace}.monitor-message{font-size:11px;line-height:1.6;max-width:210px;min-width:150px;color:#7f8974;margin-top:9px}.monitor-times{white-space:nowrap}.monitor-times span{display:block;font-size:10px;line-height:2;color:#7e8873}.monitor-alert{background:#fffbf4}.duration-alert{color:#b55230!important}.monitor-empty{text-align:center;padding:40px!important;color:#8e997f}.monitor-note{padding:0 20px 16px;font-size:10px;color:#8c977e}@media(min-width:1600px){.monitor-metrics{grid-template-columns:repeat(6,minmax(0,1fr))}}@media(max-width:760px){.monitor-heading{align-items:start;flex-direction:column}.monitor-heading h2{font-size:21px}.monitor-metrics{grid-template-columns:repeat(2,minmax(0,1fr));gap:9px}.monitor-metric{padding:14px}.monitor-metric strong{font-size:25px}.monitor-filters{padding:15px}.monitor-filters>input,.monitor-filters>label{width:100%;min-width:0}.monitor-dates input{width:calc(50% - 16px)}.monitor-selects select{width:100%;flex:auto}.monitor-statusline{flex-wrap:wrap;padding:12px 15px}.monitor-statusline small{display:block;margin-top:5px}.monitor-attention{padding:0 15px 12px}.monitor-totals{gap:10px;font-size:11px}.monitor-note{padding:0 15px 14px}}
</style>

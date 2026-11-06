<script setup lang="ts">
import {ref,onMounted,computed} from 'vue'
import {api,money,date,copy} from '../lib/api'
const emit=defineEmits<{changed:[]}>()
const props=defineProps<{admin?:boolean}>()
const data=ref<any>({items:[],stats:{}}),rules=ref<any>(null),page=ref(1),search=ref(''),busy=ref(false),saving=ref(false),error=ref(''),message=ref('')
const path=computed(()=>props.admin?'/api/admin/invites':'/api/partners')
const link=computed(()=>location.origin+'/?invite='+encodeURIComponent(data.value.invite_code||''))
async function load(initial=false){busy.value=true;error.value='';try{data.value=await api(`${path.value}?page=${page.value}&size=20&q=${encodeURIComponent(search.value)}`);if(initial)rules.value=JSON.parse(JSON.stringify(data.value.rules))}catch(e){error.value=(e as Error).message}finally{busy.value=false}}
async function save(){saving.value=true;error.value='';try{await api(path.value,{method:'PUT',body:JSON.stringify(rules.value)});message.value='规则已保存，仅影响新申请';await load(true);emit('changed')}catch(e){error.value=(e as Error).message}finally{saving.value=false}}
async function share(){try{await copy(link.value);message.value='邀请链接已复制'}catch{error.value='复制失败，请手动选择邀请链接'}}
onMounted(()=>load(true))
</script>
<template>
 <section class="partner-page">
  <p v-if="error" class="error-banner" role="alert">{{error}}</p><p v-if="message" role="status">{{message}}</p>
  <form v-if="admin&&rules" class="partner-card" @submit.prevent="save"><h2>伙伴奖励规则</h2><label><input v-model="rules.enabled" type="checkbox"/>开放邀请奖励</label><div class="partner-stages"><div v-for="(stage,i) in rules.stages" :key="i"><h3>第 {{Number(i)+1}} 阶段</h3><label>截止天数<input v-model.number="stage.until_days" type="number" min="1" max="365" required/></label><label>返积分比例（%）<input v-model.number="stage.rate_percent" type="number" min="1" max="30" required/></label></div></div><p>天数递增，比例逐段降低。新申请按被邀请人注册时长锁定规则，已有申请及奖励保留，不补发历史申请。</p><button class="button primary" :disabled="saving">{{saving?'保存中…':'保存规则'}}</button></form>
  <section v-if="!admin" class="partner-card"><h2>{{data.rules?.enabled?'邀请伙伴，持续获得奖励':'伙伴奖励暂未开放'}}</h2><div class="partner-link"><input :value="link" readonly aria-label="邀请链接"/><button class="button primary" @click="share">复制邀请链接</button></div></section>
  <section class="partner-card"><h2>奖励规则</h2><p v-for="(stage,i) in data.rules?.stages" :key="i">从伙伴注册起满 {{Number(i)===0?0:data.rules.stages[Number(i)-1].until_days}} 天至未满 {{stage.until_days}} 天，按实际结算服务费返 {{stage.rate_percent}}%。</p><p v-if="data.rules?.stages?.length">满 {{data.rules.stages[2].until_days}} 天后的新申请不再返积分。</p><p>仅直接邀请参与；通过邀请链接注册或注册时填写邀请标识绑定关系。阶段内每笔已完成且已扣服务费的申请均参与，失败和释放费用的申请不奖励。积分保留两位小数向下取整，只用于平台消费，不支持提现；历史申请不补发。</p></section>
  <div class="partner-stats"><div>邀请伙伴<strong>{{data.stats.invited||0}} 位</strong></div><div>获奖伙伴<strong>{{data.stats.qualified||0}} 位</strong></div><div>累计奖励<strong>{{money(data.stats.earned||0)}} 积分</strong></div></div>
  <form class="partner-tools" @submit.prevent="page=1;load()"><input v-if="admin" v-model="search" maxlength="100" placeholder="邀请人或被邀请人邮箱" aria-label="搜索邀请关系"/><button class="button secondary" :disabled="busy">{{admin?'搜索':'刷新'}}</button></form>
  <div class="table-scroll partner-card"><table><thead><tr><th v-if="admin">邀请人</th><th>伙伴邮箱</th><th>注册时间</th><th>获奖申请</th><th>累计奖励</th><th>最近结算</th></tr></thead><tbody><tr v-for="row in data.items" :key="row.id"><td v-if="admin">{{row.inviter}}</td><td>{{row.email}}</td><td>{{date(row.created_at)}}</td><td>{{row.orders}}</td><td>{{money(row.earned)}} 积分</td><td>{{row.rewarded_at?date(row.rewarded_at):'—'}}</td></tr><tr v-if="!data.items.length"><td :colspan="admin?6:5">{{busy?'正在读取…':'暂无邀请记录'}}</td></tr></tbody></table></div>
  <div class="partner-tools"><span>共 {{data.total||0}} 位 · 第 {{page}} 页</span><button class="button secondary" :disabled="busy||page===1" @click="page--;load()">上一页</button><button class="button secondary" :disabled="busy||page*20>=data.total" @click="page++;load()">下一页</button></div>
 </section>
</template>
<style scoped>
.partner-page{display:grid;gap:20px;min-width:0}.partner-card{border:1px solid var(--line,#e0e6de);background:white;padding:24px;border-radius:10px}.partner-card h2{font-size:18px;margin-bottom:16px}.partner-card p{color:var(--muted,#697667);font-size:13px;line-height:1.9}.partner-stages,.partner-stats{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:20px}.partner-stages label{display:grid;gap:8px;font-size:12px;margin:12px 0}.partner-stats>div{background:#edf2e8;border-radius:10px;padding:20px;font-size:13px}.partner-stats strong{display:block;font-size:24px;margin-top:12px}.partner-tools,.partner-link{display:flex;gap:12px;align-items:center}.partner-link input{min-width:0;flex:1}.partner-tools input{min-width:0;max-width:360px;flex:1}.partner-tools span{margin-right:auto;color:#687868;font-size:12px}.partner-page th,.partner-page td{white-space:nowrap}.partner-page input[type=checkbox]{width:auto}@media(max-width:650px){.partner-stages,.partner-stats{grid-template-columns:1fr}.partner-card{padding:16px}.partner-link{flex-wrap:wrap}.partner-link input{width:100%;flex-basis:100%}.partner-tools{flex-wrap:wrap}}
</style>

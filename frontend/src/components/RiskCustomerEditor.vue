<script setup lang="ts">
import {onMounted,onUnmounted,ref,watch} from 'vue';
import {api,type User} from '../lib/api';
import {ruleNames,type RiskControl} from '../lib/risk';
import Modal from './Modal.vue';
const props=defineProps<{userId?:string;label?:string}>();
const emit=defineEmits<{close:[];saved:[]}>();
const search=ref(''),users=ref<User[]>([]),selected=ref(props.userId||''),control=ref<RiskControl>(),error=ref(''),busy=ref(false),fetching=ref(false),until=ref('');
const limits=ref<Record<string,number|string>>({});let timer:ReturnType<typeof setTimeout>,sequence=0,searchSequence=0,closed=false;
async function searchUsers(){const current=++searchSequence;try{const result=await api<{items:User[]}>(`/api/admin/risk-customers?size=100&q=${encodeURIComponent(search.value)}`);if(current===searchSequence&&!closed)users.value=result.items}catch(e){if(!closed)error.value=(e as Error).message}}
async function load(){const current=++sequence;control.value=undefined;error.value='';if(!selected.value)return;fetching.value=true;try{const result=await api<RiskControl>(`/api/admin/risk-customers/${selected.value}`);if(current!==sequence||closed)return;control.value=result;limits.value={...result.limits};until.value=result.paused_until?new Date(new Date(result.paused_until).getTime()-new Date().getTimezoneOffset()*60000).toISOString().slice(0,16):''}catch(e){if(current===sequence&&!closed)error.value=(e as Error).message}finally{if(current===sequence)fetching.value=false}}
async function save(){if(!control.value)return;busy.value=true;error.value='';try{const values:Record<string,number>={};for(const [k,v] of Object.entries(limits.value))if(v!==''&&v!==null&&v!==undefined)values[k]=Number(v);await api(`/api/admin/risk-customers/${selected.value}`,{method:'PUT',body:JSON.stringify({...control.value,paused_until:control.value.paused&&until.value?new Date(until.value).toISOString():null,limits:values})});emit('saved')}catch(e){error.value=(e as Error).message}finally{busy.value=false}}
watch(search,()=>{clearTimeout(timer);timer=setTimeout(searchUsers,250)});watch(selected,load);
onMounted(()=>{searchUsers();load()});onUnmounted(()=>{closed=true;sequence++;searchSequence++;clearTimeout(timer)});
</script>
<template><Modal title="客户风控设置" wide :busy="busy" @close="!busy&&emit('close')"><form class="risk-customer" @submit.prevent="save">
 <label>查找客户<input v-model="search" aria-label="查找风控客户" placeholder="输入称呼或邮箱，最多显示 100 个匹配客户" :disabled="busy"/></label>
 <label>选择客户<select v-model="selected" aria-label="选择风控客户" :disabled="busy"><option value="">请选择</option><option v-if="selected&&!users.some(u=>u.id===selected)" :value="selected">{{props.label||'当前所选客户'}}</option><option v-for="u in users" :value="u.id" :key="u.id">{{u.name}} · {{u.email}}</option></select></label>
 <p v-if="fetching" class="muted">正在读取客户规则…</p>
 <template v-if="control"><label class="risk-check"><input v-model="control.paused" type="checkbox" :disabled="busy"/>暂停该客户提交新申请</label>
 <p class="field-help">已受理任务、查询和撤回继续执行。暂停与账户停用不同。</p>
 <label v-if="control.paused">自动恢复时间（本机时区，留空为手动恢复）<input v-model="until" aria-label="新申请自动恢复时间" type="datetime-local" :disabled="busy"/></label>
 <h3>客户单独次数配置</h3><p class="field-help">留空沿用全局次数；统计窗口、限制时长及处理模式沿用全局规则。保存后重置该客户业务接口计数，不重置登录限制。</p>
 <div class="risk-customer-grid"><template v-for="(name,key) in ruleNames" :key="key"><label v-if="key!=='login'">{{name}}<input v-model="limits[key]" :aria-label="`客户${name}次数`" type="number" min="1" max="100000" placeholder="沿用全局" :disabled="busy"/></label></template></div>
 </template><p v-if="error" class="form-error" role="alert">{{error}}</p><footer class="modal-actions"><button type="button" class="button secondary" :disabled="busy" @click="emit('close')">取消</button><button class="button primary" :disabled="busy||!control||fetching">保存客户规则</button></footer>
 </form></Modal></template>
<style scoped>.risk-customer{display:grid;gap:16px}.risk-customer label{display:grid;gap:7px;font-size:12px}.risk-customer .risk-check{display:flex;align-items:center;gap:8px}.risk-customer-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px}.risk-customer input,.risk-customer select{min-width:0;width:100%}.risk-check input{width:auto}.risk-customer h3{font-size:15px;margin:10px 0 0}@media(max-width:600px){.risk-customer-grid{grid-template-columns:1fr}}</style>

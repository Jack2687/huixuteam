<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { api } from '../lib/api';
import Icon from './Icon.vue';

type Option = { id:string; name:string; email:string };
const props=defineProps<{excludeAdmin:boolean}>();
const selected=defineModel<Option[]>({default:()=>[]});
const opened=ref(false), search=ref(''), query=ref(''), page=ref(1);
const items=ref<Option[]>([]), total=ref(0), loading=ref(false), error=ref('');
const selectedIDs=computed(()=>new Set(selected.value.map(x=>x.id)));
let controller:AbortController|undefined, timer:ReturnType<typeof setTimeout>|undefined;
async function load(){
 if(!opened.value)return;
 controller?.abort();const current=new AbortController();controller=current;
 loading.value=true;error.value='';
 try{
  const params=new URLSearchParams({q:query.value,exclude_admin:String(props.excludeAdmin),page:String(page.value)});
  const result=await api<{items:Option[];total:number}>(`/api/admin/reports/accounts/options?${params}`,{signal:current.signal});
  if(controller===current){items.value=result.items;total.value=result.total}
 }catch(e){if(controller===current&&(e as Error).name!=='AbortError')error.value=(e as Error).message}
 finally{if(controller===current)loading.value=false}
}
function toggle(option:Option){
 if(selectedIDs.value.has(option.id))selected.value=selected.value.filter(x=>x.id!==option.id);
 else if(selected.value.length<200)selected.value=[...selected.value,option];
}
watch(search,()=>{clearTimeout(timer);timer=setTimeout(()=>{query.value=search.value.trim()},250)});
watch([query,()=>props.excludeAdmin],()=>{page.value=1},{flush:'sync'});
watch([opened,query,page,()=>props.excludeAdmin],load);
onUnmounted(()=>{controller?.abort();clearTimeout(timer)});
</script>
<template>
 <details class="account-exclusions" @toggle="opened=($event.target as HTMLDetailsElement).open" @keydown.esc="($event.currentTarget as HTMLDetailsElement).open=false">
  <summary><Icon name="ListFilter" :size="14"/><span>{{selected.length?`额外排除 ${selected.length} 个账号`:'额外排除账号'}}</span><Icon name="ChevronRight" :size="13"/></summary>
  <div class="exclusion-panel">
   <div class="exclusion-title"><strong>选择不计入报表的账号</strong><button type="button" :disabled="!selected.length" @click="selected=[]">清空</button></div>
   <input v-model="search" type="search" aria-label="搜索可排除账号" placeholder="搜索账号名称或邮箱" maxlength="254"/>
   <div v-if="selected.length" class="excluded-tags"><button v-for="item in selected" :key="item.id" type="button" :title="`取消排除 ${item.email}`" @click="toggle(item)">{{item.email}}<Icon name="X" :size="12"/></button></div>
   <p v-if="error" role="alert">{{error}} <button type="button" @click="load">重试</button></p>
   <p v-else-if="loading" role="status">正在加载账号…</p>
   <div v-else class="exclusion-options" role="group" aria-label="额外排除账号多选">
    <label v-for="item in items" :key="item.id"><input type="checkbox" :checked="selectedIDs.has(item.id)" :disabled="!selectedIDs.has(item.id)&&selected.length>=200" @change="toggle(item)"/><span><strong>{{item.name}}</strong><small>{{item.email}}</small></span></label>
    <p v-if="!items.length">没有匹配的账号</p>
   </div>
   <div class="exclusion-footer"><span>已选 {{selected.length}} 个 · 最多 200 个</span><div v-if="total>50"><button type="button" :disabled="page===1||loading" @click="page--">上一页</button><span>{{page}}/{{Math.ceil(total/50)}}</span><button type="button" :disabled="page*50>=total||loading" @click="page++">下一页</button></div></div>
   <small class="exclusion-note">选中后自动更新汇总与明细，并在当前浏览器记住此管理员的选择。</small>
  </div>
 </details>
</template>
<style scoped>
.account-exclusions{position:relative;font-size:12px}.account-exclusions summary{display:flex;align-items:center;gap:7px;padding:9px 11px;border:1px solid #dce3dd;border-radius:6px;cursor:pointer;list-style:none;white-space:nowrap;background:#fff;color:#365143}.account-exclusions summary::-webkit-details-marker{display:none}.account-exclusions[open] summary{border-color:#678575;background:#f4f8f4}.exclusion-panel{position:absolute;top:calc(100% + 7px);left:0;width:330px;max-width:calc(100vw - 64px);background:white;border:1px solid #dce3dd;border-radius:10px;box-shadow:0 12px 35px #19322520;padding:14px;z-index:30}.exclusion-title,.exclusion-footer,.exclusion-footer>div{display:flex;align-items:center;justify-content:space-between;gap:8px}.exclusion-title{margin-bottom:10px}.exclusion-title strong{font-size:13px}.exclusion-panel button{color:#426650;font-size:12px}.exclusion-panel button:disabled{color:#aab3ac;cursor:default}.exclusion-panel>input{width:100%;border:1px solid #dce3dd;border-radius:6px;padding:9px 10px}.exclusion-options{max-height:240px;overflow:auto;margin:8px -5px}.exclusion-options label{display:flex;align-items:center;gap:9px;padding:9px 5px;cursor:pointer;border-radius:5px}.exclusion-options label:hover{background:#f4f7f3}.exclusion-options label>span{min-width:0;overflow-wrap:anywhere}.exclusion-options strong,.exclusion-options small{display:block}.exclusion-options strong{font-size:12px;font-weight:500}.exclusion-options small{color:#7a877c;margin-top:3px}.exclusion-options input{flex:none}.excluded-tags{display:flex;flex-wrap:wrap;gap:5px;max-height:95px;overflow:auto;margin-top:10px}.excluded-tags button{display:flex;align-items:center;gap:4px;max-width:100%;overflow-wrap:anywhere;padding:5px 7px;background:#eef5ee;border-radius:5px;text-align:left}.excluded-tags svg{flex:none}.exclusion-footer{padding-top:10px;border-top:1px solid #edf0eb;color:#7a877c}.exclusion-footer>div{gap:8px;white-space:nowrap}.exclusion-note{display:block;color:#8b968d;margin-top:10px}.exclusion-panel p{color:#7a877c;padding:12px 0}
@media(max-width:600px){.exclusion-panel{left:auto;right:0}.account-exclusions{margin-left:auto}}
</style>

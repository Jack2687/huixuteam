<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { api } from '../lib/api';
const emit=defineEmits<{toast:[message:string]}>();
const fee=ref(100),loading=ref(true),loaded=ref(false),busy=ref(false),error=ref('');
onMounted(async()=>{try{const v=await api<{default_service_fee:number}>('/api/admin/site-settings/service');fee.value=v.default_service_fee/100;loaded.value=true}catch(e){error.value=(e as Error).message}finally{loading.value=false}});
async function save(){
 if(busy.value||!loaded.value)return;
 busy.value=true;error.value='';
 try{await api('/api/admin/site-settings/service',{method:'PUT',body:JSON.stringify({default_service_fee:Math.round(Number(fee.value)*100)})});emit('toast','注册默认服务单价已保存')}catch(e){error.value=(e as Error).message}finally{busy.value=false}
}
</script>
<template>
 <form class="service-pricing" @submit.prevent="save">
  <h3>注册默认服务单价</h3>
  <p>新用户注册时使用此单价。修改后仅影响之后注册的用户，已有用户的单价保持不变。</p>
  <div v-if="loading" class="loading">正在加载服务定价…</div>
  <label v-else>服务单价（积分 / 单）<input v-model.number="fee" aria-label="注册默认服务单价" type="number" min="0" max="100000" step="0.01" required :disabled="!loaded||busy"/></label>
  <p v-if="error" class="error-banner" role="alert">{{error}}</p>
  <footer><button class="button primary" :disabled="loading||!loaded||busy">{{busy?'保存中…':'保存服务单价'}}</button></footer>
 </form>
</template>
<style scoped>
.service-pricing{padding:26px;background:#fff;border:1px solid var(--line);border-radius:8px;min-width:0}.service-pricing h3{font-size:17px;margin-bottom:10px}.service-pricing>p{font-size:12px;line-height:1.8;color:var(--muted)}.service-pricing label{display:grid;gap:10px;font-size:12px;max-width:360px;margin:22px 0}.service-pricing input{width:100%;min-width:0}.service-pricing footer{display:flex;justify-content:flex-end;border-top:1px solid var(--line);padding-top:20px}@media(max-width:480px){.service-pricing{padding:20px}}
</style>

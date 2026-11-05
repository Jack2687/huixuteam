<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue';
import { api } from '../lib/api';
import type { CustomMenu } from '../lib/site';
import Icon from './Icon.vue';
const props=defineProps<{id:string;refresh:number}>();
const item=ref<CustomMenu>(),error=ref(''),loading=ref(true);
let request=0;
async function load(){
 const version=++request;
 if(item.value?.id!==props.id){item.value=undefined;loading.value=true}
 try{
  const result=await api<CustomMenu>('/api/workspace/custom-menus/'+encodeURIComponent(props.id));
  if(version!==request)return;item.value=result;error.value='';
 }catch(e){if(version===request){item.value=undefined;error.value=(e as Error).message}}
 finally{if(version===request)loading.value=false}
}
watch(()=>[props.id,props.refresh],load,{immediate:true});onUnmounted(()=>request++);
</script>
<template>
 <section class="custom-page">
  <p v-if="loading" class="loading">正在加载页面…</p>
  <p v-else-if="error" class="error-banner" role="alert">{{error}}</p>
  <template v-else-if="item"><header v-if="!item.hide_open_button||item.open_in_new_tab" class="custom-page-toolbar"><span>{{item.open_in_new_tab?'此菜单在新窗口中打开':'页面由外部网站提供'}}</span><a :href="`/workspace/custom/${item.id}/open`" target="_blank" rel="noopener noreferrer" class="button secondary">新窗口打开<Icon name="ExternalLink" :size="16"/></a></header><iframe v-if="!item.open_in_new_tab" :key="item.id" :src="item.url" :title="item.label" referrerpolicy="no-referrer" sandbox="allow-scripts allow-forms allow-popups allow-popups-to-escape-sandbox allow-downloads allow-modals"/></template>
 </section>
</template>
<style scoped>
.custom-page{border:1px solid var(--line);border-radius:8px;background:#fff;overflow:hidden;min-height:160px}.custom-page-toolbar{display:flex;align-items:center;justify-content:space-between;gap:16px;padding:14px 20px;border-bottom:1px solid var(--line);font-size:12px;color:var(--muted)}.custom-page iframe{display:block;width:100%;height:max(540px,calc(100dvh - 290px));border:0}.custom-page .error-banner{margin:18px}@media(max-width:720px){.custom-page-toolbar{padding:12px}.custom-page-toolbar span{font-size:11px}.custom-page iframe{height:calc(100dvh - 220px);min-height:460px}}
</style>

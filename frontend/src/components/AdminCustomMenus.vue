<script setup lang="ts">
import { ref } from 'vue';
import { api } from '../lib/api';
import { requestID } from '../lib/request-id';
import type { CustomMenu } from '../lib/site';
import Icon from './Icon.vue';

const props=defineProps<{ initial:CustomMenu[] }>();
const emit=defineEmits<{saved:[items:CustomMenu[]];toast:[message:string]}>();
const items=ref<CustomMenu[]>(props.initial.map(item=>({...item})));
const collapsed=ref(new Set<string>()),busy=ref(false),error=ref('');
const svgSrc=(svg:string)=>'data:image/svg+xml;charset=utf-8,'+encodeURIComponent(svg);
function add(){
 if(items.value.length>=20)return;
 items.value.push({id:requestID(),label:'',url:'',icon_svg:'',visibility:'user',enabled:true,open_in_new_tab:false,hide_open_button:false,sort_order:items.value.length});
}
function move(index:number,direction:number){
 const target=index+direction;
 if(target<0||target>=items.value.length)return;
 const item=items.value.splice(index,1)[0]!;items.value.splice(target,0,item);
}
function toggle(id:string){collapsed.value.has(id)?collapsed.value.delete(id):collapsed.value.add(id)}
async function upload(event:Event,item:CustomMenu){
 const input=event.target as HTMLInputElement,file=input.files?.[0];if(!file)return;
 error.value='';
 try{
  if(!file.name.toLowerCase().endsWith('.svg')||file.size>16*1024)throw new Error('请选择不超过16KB的SVG图标');
  const raw=await file.text();
  if(new DOMParser().parseFromString(raw,'image/svg+xml').querySelector('parsererror'))throw new Error('SVG文件格式不正确');
  item.icon_svg=raw;
 }catch(e){error.value=(e as Error).message}finally{input.value=''}
}
async function save(){
 if(busy.value)return;busy.value=true;error.value='';
 try{
  const value=items.value.map((item,index)=>({...item,sort_order:index}));
  await api('/api/admin/site-settings/custom-menus',{method:'PUT',body:JSON.stringify(value)});
  const result=await api<{custom_menus:CustomMenu[]}>('/api/admin/site-settings');
  items.value=result.custom_menus;emit('saved',result.custom_menus);emit('toast','自定义菜单已保存');
 }catch(e){error.value=(e as Error).message}finally{busy.value=false}
}
</script>
<template>
 <form class="custom-editor" @submit.prevent="save">
  <div class="custom-intro"><div><h3>自定义菜单</h3><p>将常用页面加入侧边栏，设置可见角色和打开方式。</p></div><span>{{items.length}} / 20</span></div>
  <p v-if="error" class="error-banner" role="alert">{{error}}</p>
  <div v-if="!items.length" class="menu-empty"><Icon name="ExternalLink" :size="28"/><h4>添加你的第一个菜单</h4><p>例如充值中心、帮助文档或服务入口。</p></div>
  <section v-for="(item,index) in items" :key="item.id" class="menu-card" :aria-label="`菜单项 ${index+1}`">
   <header class="menu-card-heading"><div class="menu-card-title"><span>{{String(index+1).padStart(2,'0')}}</span><strong>{{item.label||'未命名菜单'}}</strong><small v-if="!item.enabled">已停用</small></div><div class="menu-actions"><button type="button" class="icon-button" :disabled="busy||index===0" :aria-label="`上移菜单 ${index+1}`" title="上移" @click="move(index,-1)">↑</button><button type="button" class="icon-button" :disabled="busy||index===items.length-1" :aria-label="`下移菜单 ${index+1}`" title="下移" @click="move(index,1)">↓</button><button type="button" class="icon-button" :aria-label="`展开或收起菜单 ${index+1}`" :aria-expanded="!collapsed.has(item.id)" @click="toggle(item.id)"><Icon name="ChevronRight" :class="{expanded:!collapsed.has(item.id)}" :size="16"/></button><button type="button" class="icon-button danger" :disabled="busy" :aria-label="`删除菜单 ${index+1}`" title="删除菜单" @click="items.splice(index,1)"><Icon name="X" :size="16"/></button></div></header>
   <div v-show="!collapsed.has(item.id)" class="menu-card-body"><div class="menu-fields"><label>菜单名称<input v-model="item.label" required maxlength="60" placeholder="例如：充值中心"/></label><label>可见角色<select v-model="item.visibility"><option value="user">普通用户（管理员也可见）</option><option value="admin">仅管理员</option></select></label><label class="menu-url">页面 URL<input v-model="item.url" required type="url" maxlength="2048" placeholder="https://example.com"/></label></div>
    <div class="menu-options"><label><input v-model="item.enabled" type="checkbox"/>启用菜单</label><label><input v-model="item.open_in_new_tab" type="checkbox"/>点击菜单时新窗口打开</label><label :class="{muted:item.open_in_new_tab}"><input v-model="item.hide_open_button" type="checkbox" :disabled="item.open_in_new_tab"/>隐藏内嵌页的“新窗口打开”按钮</label></div>
    <p class="menu-hint">{{item.open_in_new_tab?'点击侧边栏菜单直接打开新标签页，当前工作台保留。':'点击侧边栏菜单后，在工作台内嵌显示页面。若目标网站不支持内嵌，请选择新窗口打开。'}}</p>
    <div class="menu-icon-row"><div class="menu-icon-preview"><img v-if="item.icon_svg" :src="svgSrc(item.icon_svg)" alt="菜单图标预览"/><Icon v-else name="ExternalLink" :size="23"/></div><div><span class="menu-icon-label">SVG 图标</span><div class="menu-icon-actions"><label class="button secondary"><Icon name="Upload" :size="15"/>上传 SVG<input class="sr-only" type="file" accept=".svg,image/svg+xml" :aria-label="`上传菜单 ${index+1} SVG`" @change="upload($event,item)"/></label><button type="button" class="text-button danger" :disabled="!item.icon_svg" @click="item.icon_svg=''">清除图标</button><small>最多 16KB，未上传时使用默认图标</small></div></div></div>
   </div>
  </section>
  <button type="button" class="menu-add" :disabled="busy||items.length>=20" @click="add"><Icon name="Plus" :size="17"/>添加菜单</button>
  <footer class="custom-save"><span>调整顺序、删除或修改后，点击保存生效。</span><button class="button primary" :disabled="busy">{{busy?'保存中…':'保存自定义菜单'}}</button></footer>
 </form>
</template>
<style scoped>
.custom-editor{display:grid;gap:18px;background:#fff;border:1px solid var(--line);border-radius:8px;padding:26px;min-width:0}.custom-intro{display:flex;align-items:center;justify-content:space-between;gap:16px}.custom-intro h3{font-size:17px;margin-bottom:8px}.custom-intro p,.custom-intro>span{font-size:12px;color:var(--muted)}.menu-card{border:1px solid var(--line);border-radius:7px;min-width:0}.menu-card-heading{display:flex;justify-content:space-between;gap:12px;align-items:center;padding:13px 17px;background:#fafaf7;border-radius:7px}.menu-card-title{display:flex;align-items:center;gap:12px;min-width:0;font-size:13px}.menu-card-title>span{font:11px monospace;color:var(--muted)}.menu-card-title strong{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.menu-card-title small{white-space:nowrap;color:var(--muted);font-size:10px}.menu-actions{display:flex;gap:2px}.menu-actions .icon-button{height:28px;width:28px}.expanded{transform:rotate(90deg)}.menu-card-body{padding:18px}.menu-fields{display:grid;grid-template-columns:1fr 1fr;gap:16px}.menu-fields label{display:grid;gap:9px;font-size:12px;min-width:0}.menu-fields input,.menu-fields select{width:100%;font-size:13px}.menu-url{grid-column:1/-1}.menu-options{display:flex;gap:16px;flex-wrap:wrap;margin:19px 0 9px;font-size:12px}.menu-options label{display:flex;align-items:center;gap:8px}.menu-hint{font-size:11px;color:var(--muted);margin-bottom:20px}.menu-icon-row{display:flex;align-items:center;gap:14px;flex-wrap:wrap}.menu-icon-preview{display:flex;align-items:center;justify-content:center;width:54px;height:54px;border:1px dashed #cdd1c4;background:#f8f8f3;border-radius:8px;color:var(--accent)}.menu-icon-preview img{width:26px;height:26px;object-fit:contain}.menu-icon-label{font-size:11px;color:var(--muted)}.menu-icon-actions{display:flex;align-items:center;gap:14px;flex-wrap:wrap;margin-top:8px}.menu-icon-actions .button{font-size:11px;min-height:32px;padding:7px 10px;cursor:pointer}.menu-icon-actions small{font-size:10px;color:var(--muted)}.menu-add{display:flex;align-items:center;justify-content:center;gap:8px;border:1px dashed #cdd1c4;border-radius:6px;min-height:44px;color:var(--accent);font-size:12px}.menu-add:hover{background:#f8f8f3}.custom-save{display:flex;align-items:center;justify-content:space-between;gap:16px;border-top:1px solid var(--line);padding-top:20px}.custom-save span{font-size:11px;color:var(--muted)}.menu-empty{text-align:center;padding:32px;color:var(--muted);font-size:12px}.menu-empty h4{margin:12px 0 6px;color:var(--ink)}@media(max-width:720px){.custom-editor{padding:18px}.menu-fields{grid-template-columns:1fr}.menu-card-body{padding:14px}.menu-card-heading{padding:12px}.menu-options{display:grid;gap:13px}.custom-save span{display:none}.custom-save{justify-content:flex-end}.custom-intro p{font-size:11px}}
</style>

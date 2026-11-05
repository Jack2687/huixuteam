<script setup lang="ts">
import { computed, ref } from 'vue';
import Icon from './Icon.vue';
import { api } from '../lib/api';
import { applyBranding } from '../lib/branding';
import type { SiteBranding } from '../lib/site';
const props=defineProps<{initial:SiteBranding}>();
const emit=defineEmits<{toast:[message:string];saved:[value:SiteBranding]}>();
const file=ref<File>(),draft=ref(''),error=ref(''),busy=ref(false),reading=ref(false),input=ref<HTMLInputElement>();
const preview=computed(()=>draft.value||props.initial.icon_url);
let selection=0;
function clear(){selection++;file.value=undefined;draft.value='';reading.value=false;if(input.value)input.value.value=''}
async function choose(event:Event){
 const chosen=(event.target as HTMLInputElement).files?.[0];clear();error.value='';
 if(!chosen)return;
 if(!/\.(png|jpe?g|svg)$/i.test(chosen.name)){error.value='请选择 PNG、JPG 或 SVG 图标';return}
 const svg=/\.svg$/i.test(chosen.name);
 if(!chosen.size||chosen.size>(svg?16*1024:1024*1024)){error.value=svg?'SVG 图标须小于 16KB':'图片不能为空或超过 1MB';return}
 const current=selection;reading.value=true;
 try{
  const data=await new Promise<string>((resolve,reject)=>{const r=new FileReader();r.onload=()=>resolve(String(r.result));r.onerror=()=>reject(new Error('无法读取图标，请重新选择'));r.readAsDataURL(chosen)});
  if(current!==selection)return;
  const image=new Image();image.src=data;
  await image.decode();
  if(current!==selection)return;
  if(!svg&&(image.width>2048||image.height>2048))throw new Error('图片宽高不能超过 2048 像素');
  file.value=chosen;draft.value=data;
 }catch(e){if(current===selection)error.value=e instanceof Error&&e.message?e.message:'图片无法预览，请选择有效图标'}finally{if(current===selection)reading.value=false}
}
async function save(reset=false){
 if(busy.value||reading.value||(!reset&&!file.value))return;
 busy.value=true;error.value='';
 try{
  const value=await api<SiteBranding>('/api/admin/site-settings/branding',reset?{method:'DELETE'}:{method:'PUT',headers:{'Content-Type':'application/octet-stream'},body:file.value});
  applyBranding(value);clear();emit('saved',value);emit('toast',reset?'已恢复回序默认图标':'站点图标已保存');
 }catch(e){error.value=(e as Error).message}finally{busy.value=false}
}
</script>
<template>
 <section class="brand-settings-card">
  <div class="brand-settings-title"><div><span class="eyebrow">BRAND / IDENTITY</span><h3>让服务拥有自己的标记</h3><p>一枚图标，同步用于登录页、服务台导航和浏览器标签。</p></div><span class="brand-kind">{{initial.customized?'自定义图标':'回序默认图标'}}</span></div>
  <div class="brand-editor">
   <div class="brand-preview"><div class="brand-preview-dark"><img :src="preview" alt="站点图标预览"/><div><strong>回序 <small>HUIXU</small></strong><span>服务有序 · 进度有据</span></div></div><div class="brand-preview-tab"><span class="brand-preview-dot"/><img :src="preview" alt="浏览器标签图标预览"/><span>回序 HUIXU · 服务台</span><Icon name="X" :size="12"/></div><p>侧栏与浏览器标签预览</p></div>
   <div class="brand-upload"><h4>站点图标</h4><p>建议使用正方形图标，推荐 256 × 256 像素。透明背景也可使用。</p><input ref="input" class="brand-file-input" type="file" accept="image/png,image/jpeg,image/svg+xml,.svg" aria-label="上传站点图标" :disabled="busy||reading" @change="choose"/><button class="button secondary" :disabled="busy||reading" @click="input?.click()"><Icon name="Upload" :size="16"/>{{reading?'读取中…':'选择图标文件'}}</button><small>PNG / JPG 最大 1MB、宽高最大 2048px；静态 SVG 最大 16KB。</small><div v-if="file" class="brand-file-name"><Icon name="Check" :size="15"/><span>{{file.name}}</span><button class="text-button" :disabled="busy" @click="clear">取消选择</button></div><p class="brand-save-hint">选择后点击保存生效。恢复默认会重新使用回序图标。</p></div>
  </div>
  <p v-if="error" class="error-banner" role="alert">{{error}}</p>
  <footer class="brand-settings-save"><button class="text-button" :disabled="busy||reading||(!initial.customized&&!file)" @click="save(true)"><Icon name="RotateCcw" :size="15"/>恢复默认图标</button><button class="button primary" :disabled="busy||reading||!file" @click="save()">{{busy?'保存中…':'保存图标'}}</button></footer>
 </section>
</template>
<style scoped>
.brand-settings-card{background:#fff;border:1px solid var(--line);border-radius:8px;padding:28px;min-width:0}.brand-settings-title{display:flex;justify-content:space-between;gap:16px;align-items:flex-start}.brand-settings-title h3{font-size:21px;margin:10px 0}.brand-settings-title p,.brand-upload p{font-size:12px;color:var(--muted);line-height:1.8}.brand-kind{white-space:nowrap;font-size:11px;background:#f1f3e9;color:#64754f;padding:7px 10px;border-radius:4px}.brand-editor{display:grid;grid-template-columns:minmax(230px,360px) minmax(0,1fr);gap:32px;margin:28px 0}.brand-preview{padding:22px;background:#f4f3ee;border:1px solid #e5e7dd;border-radius:8px;align-self:start}.brand-preview-dark{display:flex;align-items:center;gap:14px;background:#263a32;color:#f5eee2;border-radius:8px;padding:26px 20px}.brand-preview-dark img{width:56px;height:56px;object-fit:contain;border-radius:12px}.brand-preview-dark strong{font-size:22px;white-space:nowrap;letter-spacing:2px}.brand-preview-dark small{font-size:9px}.brand-preview-dark span{display:block;font-size:9px;opacity:.6;margin-top:6px}.brand-preview-tab{display:flex;align-items:center;gap:8px;margin-top:18px;padding:12px;background:white;border:1px solid #e2e5da;border-radius:6px;font-size:10px}.brand-preview-tab img{width:16px;height:16px;object-fit:contain}.brand-preview-tab>span:nth-last-child(2){flex:1}.brand-preview-dot{height:6px;width:6px;background:#c9cec0;border-radius:50%}.brand-preview>p{text-align:center;font-size:10px;color:var(--muted);margin:14px 0 0}.brand-upload{padding:8px 0;min-width:0}.brand-upload h4{font-size:15px;margin-bottom:12px}.brand-upload .button{margin:19px 0 12px}.brand-upload>small{display:block;font-size:11px;color:var(--muted);line-height:1.8}.brand-file-input{position:absolute;width:1px;height:1px;padding:0;opacity:0;overflow:hidden;clip-path:inset(50%)}.brand-file-name{display:flex;gap:8px;align-items:center;margin-top:18px;font-size:12px;color:#536a41}.brand-file-name span{overflow-wrap:anywhere;min-width:0;flex:1}.brand-file-name button{flex-shrink:0}.brand-save-hint{margin-top:20px}.brand-settings-save{display:flex;justify-content:space-between;gap:15px;border-top:1px solid var(--line);padding-top:20px}.brand-settings-save>.text-button{display:flex;align-items:center;gap:7px}@media(max-width:850px){.brand-editor{grid-template-columns:1fr}.brand-preview{max-width:360px}.brand-settings-title{flex-wrap:wrap}}@media(max-width:480px){.brand-settings-card{padding:20px}.brand-settings-title h3{font-size:18px}.brand-preview{padding:14px}.brand-preview-dark{padding:20px 12px}.brand-settings-save{flex-wrap:wrap}}
</style>

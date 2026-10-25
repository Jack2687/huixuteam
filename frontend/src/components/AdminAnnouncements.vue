<script setup lang="ts">
import { ref, watch } from 'vue';
import { api, date, announcementCategories, type Announcement, type Page } from '../lib/api';
import Icon from './Icon.vue';
import Modal from './Modal.vue';
import Pagination from './Pagination.vue';
const emit=defineEmits<{toast:[message:string];changed:[]}>();
const items=ref<Announcement[]>([]),page=ref(1),size=ref(20),total=ref(0),loading=ref(false),busy=ref(false),error=ref(''),formError=ref(''),editing=ref(false),removing=ref<Announcement>();
const blank=()=>({id:'',title:'',content:'',category:'notice' as Announcement['category'],published:false,pinned:false});
const form=ref(blank());
async function load(){loading.value=true;error.value='';try{const d=await api<Page<Announcement>>(`/api/admin/announcements?page=${page.value}&size=${size.value}`);items.value=d.items;total.value=d.total;if(page.value>Math.max(1,Math.ceil(total.value/size.value)))page.value=Math.max(1,Math.ceil(total.value/size.value))}catch(e){error.value=(e as Error).message}finally{loading.value=false}}
function edit(a?:Announcement){form.value=a?{id:a.id,title:a.title,content:a.content,category:a.category,published:a.published,pinned:a.pinned}:blank();formError.value='';editing.value=true}
async function save(){
 busy.value=true;formError.value='';
 try{const {id,...body}=form.value;await api('/api/admin/announcements'+(id?`/${id}`:''),{method:id?'PUT':'POST',body:JSON.stringify(body)});editing.value=false;emit('toast',body.published?'公告已发布，用户可查看':'草稿已保存');emit('changed');await load()}
 catch(e){formError.value=(e as Error).message}finally{busy.value=false}
}
async function toggle(a:Announcement,field:'published'|'pinned'){
 busy.value=true;error.value='';
 try{await api(`/api/admin/announcements/${a.id}`,{method:'PUT',body:JSON.stringify({title:a.title,content:a.content,category:a.category,published:a.published,pinned:a.pinned,[field]:!a[field]})});emit('toast',field==='published'?(a.published?'公告已撤下':'公告已发布'):(a.pinned?'已取消置顶':'已置顶公告'));emit('changed');await load()}
 catch(e){error.value=(e as Error).message}finally{busy.value=false}
}
async function remove(){if(!removing.value)return;busy.value=true;formError.value='';try{await api(`/api/admin/announcements/${removing.value.id}`,{method:'DELETE'});removing.value=undefined;emit('toast','公告已删除');emit('changed');await load()}catch(e){formError.value=(e as Error).message}finally{busy.value=false}}
watch(()=>[page.value,size.value],load,{immediate:true});
</script>
<template>
 <section class="table-card">
  <div class="section-title"><div><span class="eyebrow">PUBLISHING</span><h2>公告发布台</h2><p>先保存草稿，确认内容后再发布；置顶内容优先展示。</p></div><button class="button primary" :disabled="busy" @click="edit()"><Icon name="Plus"/>撰写公告</button></div>
  <div v-if="error" class="error-banner" role="alert">{{error}}<button @click="load">再次读取</button></div>
  <div class="table-scroll"><table><thead><tr><th>公告内容</th><th>分类</th><th>发布状态</th><th>最近编辑</th><th>管理操作</th></tr></thead><tbody>
   <tr v-for="a in items" :key="a.id"><td class="announcement-title-cell"><span v-if="a.pinned" class="badge amber">置顶</span><strong>{{a.title}}</strong><small>{{a.content.slice(0,65)}}</small></td><td>{{announcementCategories[a.category]}}</td><td><span class="badge" :class="a.published?'green':'gray'">{{a.published?'已发布':'草稿'}}</span></td><td class="tabular muted">{{date(a.updated_at)}}</td><td><div class="admin-row-actions"><button class="text-button" :disabled="busy" @click="edit(a)">编辑</button><button class="text-button" :disabled="busy" @click="toggle(a,'published')">{{a.published?'撤下':'发布'}}</button><button class="text-button" :disabled="busy" @click="toggle(a,'pinned')">{{a.pinned?'取消置顶':'置顶'}}</button><button class="text-button danger" :disabled="busy" @click="removing=a;formError=''">删除</button></div></td></tr>
  </tbody></table></div>
  <div v-if="!items.length" class="empty"><Icon name="Bell" :size="30"/><h3>{{loading?'正在读取…':'开始撰写第一篇公告'}}</h3><p>草稿仅管理人员可见，发布后显示在用户公告中心。</p></div>
  <Pagination v-model:page="page" v-model:size="size" :total="total" label="公告管理分页"/>
 </section>
 <Modal v-if="editing" :title="form.id?'编辑公告':'撰写公告'" description="用户会看到标题、正文、分类与发布时间。" wide :busy="busy" @close="!busy&&(editing=false)">
  <form @submit.prevent="save"><div class="modal-body">
   <label>公告标题<input v-model="form.title" required maxlength="120" placeholder="一句话概括这条消息"/></label>
   <label>公告分类<select v-model="form.category"><option v-for="(label,key) in announcementCategories" :key="key" :value="key">{{label}}</option></select></label>
   <label>公告正文<textarea v-model="form.content" required maxlength="10000" rows="9" placeholder="填写需要告知用户的内容，支持换行。"/></label>
   <div class="form-row"><label class="checkbox-label"><input v-model="form.published" type="checkbox"/>保存后公开发布</label><label class="checkbox-label"><input v-model="form.pinned" type="checkbox"/>在公告列表置顶</label></div>
   <p v-if="formError" class="form-error" role="alert">{{formError}}</p>
  </div><footer class="modal-actions"><button type="button" class="button secondary" :disabled="busy" @click="editing=false">暂不保存</button><button class="button primary" :disabled="busy">{{busy?'保存中…':form.published?'保存并发布':'保存草稿'}}</button></footer></form>
 </Modal>
 <Modal v-if="removing" title="删除这篇公告？" :busy="busy" @close="!busy&&(removing=undefined)"><div class="modal-body"><p>「{{removing.title}}」将从公告列表移除。只需暂时隐藏时，请使用“撤下”。</p><p v-if="formError" class="form-error">{{formError}}</p></div><footer class="modal-actions"><button class="button secondary" :disabled="busy" @click="removing=undefined">保留公告</button><button class="button primary" :disabled="busy" @click="remove">确认删除</button></footer></Modal>
</template>

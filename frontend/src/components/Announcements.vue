<script setup lang="ts">
import { ref, watch, onUnmounted } from 'vue';
import { api, date, announcementCategories, type Announcement, type Page } from '../lib/api';
import Icon from './Icon.vue';
import Pagination from './Pagination.vue';
import AnnouncementTicker from './AnnouncementTicker.vue';
import Modal from './Modal.vue';
const props=defineProps<{compact?:boolean;login?:boolean;refresh:number}>();
const emit=defineEmits<{all:[]}>();
const items=ref<Announcement[]>([]),total=ref(0),page=ref(1),size=ref(10),loading=ref(false),error=ref('');
const selected=ref<Announcement>();
let controller:AbortController|undefined;
async function load(){
 controller?.abort();const c=new AbortController();controller=c;loading.value=true;error.value='';
 try{
  const endpoint=props.login?'/api/public/announcements':'/api/announcements';
  const data=await api<Page<Announcement>>(`${endpoint}?page=${page.value}&size=${props.compact||props.login?100:size.value}`,{signal:c.signal});
  if(controller===c){
   items.value=data.items;total.value=data.total;
  }
 }
 catch(e){if((e as Error).name!=='AbortError')error.value=(e as Error).message}
 finally{if(controller===c)loading.value=false}
}
watch(()=>[props.refresh,page.value,size.value],load,{immediate:true});
onUnmounted(()=>controller?.abort());
</script>
<template>
 <section v-if="login" class="login-announcements" aria-label="登录页公告">
  <header><span><Icon name="Bell" :size="16"/>平台公告</span><small>点击查看完整公告</small></header>
  <div v-if="items.length" class="login-announcement-ticker"><AnnouncementTicker :items="items" @open="selected=$event"/></div>
  <p v-else class="login-announcement-empty">{{error?'公告暂未加载':loading?'正在读取公告…':'暂无新公告'}}<button v-if="error" class="text-button" @click="load">重试</button></p>
 </section>
 <section v-else-if="compact" class="announcement-strip" aria-label="平台公告">
  <span class="announcement-strip-label"><Icon name="Bell" :size="16"/>平台公告</span>
  <div v-if="items.length" class="announcement-carousel"><AnnouncementTicker :items="items" @open="selected=$event"/></div>
  <span v-else class="muted">{{error?'公告暂未加载':loading?'读取公告…':'暂无新公告'}}</span>
  <button class="text-button" @click="emit('all')">公告中心<Icon name="ArrowUpRight" :size="14"/></button>
 </section>
 <section v-else class="announcements-page">
  <div class="bulletin-intro"><div><span class="eyebrow">HUIXU / BULLETIN</span><h2>服务消息，从这里了解。</h2><p>产品调整、服务通知与维护计划，统一发布在这里。</p></div><span class="bulletin-count">{{total}}<small>篇公告</small></span></div>
  <div v-if="error" class="error-banner" role="alert">{{error}}<button @click="load">再次读取</button></div>
  <div v-if="loading&&!items.length" class="loading"><Icon name="LoaderCircle" class="spin"/>正在读取公告</div>
  <div v-else-if="!items.length&&!error" class="empty"><Icon name="Bell" :size="32"/><h3>还没有发布公告</h3><p>后续服务消息会显示在此处。</p></div>
  <article v-for="item in items" :key="item.id" class="bulletin-card" :class="{pinned:item.pinned}">
   <div class="bulletin-meta"><span class="badge" :class="item.category==='maintenance'?'amber':'gray'">{{announcementCategories[item.category]}}</span><span v-if="item.pinned" class="badge amber">置顶</span><time>{{date(item.published_at!)}}</time></div>
   <h2>{{item.title}}</h2><p class="bulletin-body">{{item.content}}</p>
  </article>
  <Pagination v-model:page="page" v-model:size="size" :total="total" :sizes="[10,20,50]" label="公告分页"/>
 </section>
 <Modal v-if="selected" :title="selected.title" wide @close="selected=undefined">
  <div class="modal-body announcement-detail"><div class="bulletin-meta"><span class="badge gray">{{announcementCategories[selected.category]}}</span><time>{{date(selected.published_at!)}}</time></div><p>{{selected.content}}</p></div>
  <footer class="modal-actions"><button type="button" class="button secondary" @click="selected=undefined">关闭</button></footer>
 </Modal>
</template>
<style scoped>
.login-announcements{margin-top:30px;border:1px solid #ded8cb;border-radius:6px;background:#fffaf2;min-width:0;overflow:hidden}
.login-announcements>header{display:flex;align-items:center;justify-content:space-between;gap:10px;padding:13px 16px;border-bottom:1px solid #eae2d4}
.login-announcements>header>span{display:flex;align-items:center;gap:7px;color:#795d35;font-size:12px;font-weight:600}
.login-announcements>header>small{color:#918572;font-size:10px}
.login-announcement-ticker{padding:12px 16px;min-width:0}
.login-announcement-empty{padding:16px;color:#918572;font-size:12px;display:flex;justify-content:space-between;gap:10px}
.announcement-carousel{flex:1;min-width:0}
.announcement-detail p{white-space:pre-wrap;overflow-wrap:anywhere;line-height:1.9}
@media(max-width:640px){.announcement-strip{flex-wrap:wrap}.announcement-carousel{order:3;flex-basis:100%}.announcement-strip>.text-button{margin-left:auto}}
</style>

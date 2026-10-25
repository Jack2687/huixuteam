<script setup lang="ts">
import { ref, watch, nextTick, onUnmounted } from 'vue';
import { api, date, announcementCategories, type Announcement, type Page } from '../lib/api';
import Icon from './Icon.vue';
import Pagination from './Pagination.vue';
const props=defineProps<{compact?:boolean;login?:boolean;refresh:number}>();
const emit=defineEmits<{all:[]}>();
const items=ref<Announcement[]>([]),total=ref(0),page=ref(1),size=ref(10),loading=ref(false),error=ref('');
const slides=ref<HTMLElement>(),active=ref(0);
let controller:AbortController|undefined;
function alignSlide(){
 const el=slides.value;
 if(el)el.scrollTo({left:active.value*el.clientWidth,behavior:'auto'});
}
function step(direction:number){
 const el=slides.value;
 if(!el)return;
 const target=Math.max(0,Math.min(items.value.length-1,active.value+direction));
 el.scrollTo({left:target*el.clientWidth,behavior:window.matchMedia('(prefers-reduced-motion: reduce)').matches?'auto':'smooth'});
}
function trackSlide(){
 const el=slides.value;
 if(el?.clientWidth)active.value=Math.max(0,Math.min(items.value.length-1,Math.round(el.scrollLeft/el.clientWidth)));
}
watch(slides,(el,_,onCleanup)=>{
 if(!el)return;
 const observer=new ResizeObserver(alignSlide);
 observer.observe(el);
 onCleanup(()=>observer.disconnect());
});
async function load(){
 controller?.abort();const c=new AbortController();controller=c;loading.value=true;error.value='';
 try{
  const endpoint=props.login?'/api/public/announcements':'/api/announcements';
  const data=await api<Page<Announcement>>(`${endpoint}?page=${page.value}&size=${props.compact||props.login?100:size.value}`,{signal:c.signal});
  if(controller===c){
   const currentID=items.value[active.value]?.id;
   items.value=data.items;total.value=data.total;
   const position=data.items.findIndex(item=>item.id===currentID);
   active.value=position>=0?position:Math.max(0,Math.min(active.value,data.items.length-1));
   await nextTick();
   if(controller===c)alignSlide();
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
  <header><span><Icon name="Bell" :size="16"/>平台公告</span><div v-if="items.length>1" class="announcement-controls">
   <button class="announcement-arrow" aria-label="上一条公告" :disabled="active===0" @click="step(-1)"><Icon name="ChevronLeft" :size="16"/></button>
   <span class="announcement-position" aria-live="polite" aria-atomic="true">{{active+1}} / {{items.length}}</span>
   <button class="announcement-arrow" aria-label="下一条公告" :disabled="active===items.length-1" @click="step(1)"><Icon name="ChevronRight" :size="16"/></button>
  </div></header>
  <div v-if="items.length" ref="slides" class="announcement-slides login-announcement-slides" role="group" aria-label="左右滚动查看公告" @scroll="trackSlide" @keydown.left.prevent="step(-1)" @keydown.right.prevent="step(1)">
   <article v-for="(item,index) in items" :key="item.id" :tabindex="index===active?0:-1" :aria-hidden="index!==active" class="login-announcement-card"><div class="bulletin-meta"><span v-if="item.pinned" class="badge amber">置顶</span><span>{{announcementCategories[item.category]}}</span><time>{{date(item.published_at!)}}</time></div><h3>{{item.title}}</h3><p>{{item.content}}</p></article>
  </div>
  <p v-else class="login-announcement-empty">{{error?'公告暂未加载':loading?'正在读取公告…':'暂无新公告'}}<button v-if="error" class="text-button" @click="load">重试</button></p>
 </section>
 <section v-else-if="compact" class="announcement-strip" aria-label="平台公告">
  <span class="announcement-strip-label"><Icon name="Bell" :size="16"/>平台公告</span>
  <div v-if="items.length" class="announcement-carousel">
   <div ref="slides" class="announcement-slides" role="group" aria-label="左右滚动查看公告" @scroll="trackSlide" @keydown.left.prevent="step(-1)" @keydown.right.prevent="step(1)">
    <button v-for="(item,index) in items" :key="item.id" class="announcement-preview" :tabindex="index===active?0:-1" :aria-hidden="index!==active" :title="item.title" @click="emit('all')"><span v-if="item.pinned" class="badge amber">置顶</span><strong>{{item.title}}</strong><time>{{date(item.published_at!)}}</time></button>
   </div>
  </div>
  <span v-else class="muted">{{error?'公告暂未加载':loading?'读取公告…':'暂无新公告'}}</span>
  <div v-if="items.length>1" class="announcement-controls">
   <button class="announcement-arrow" aria-label="上一条公告" :disabled="active===0" @click="step(-1)"><Icon name="ChevronLeft" :size="16"/></button>
   <span class="announcement-position" aria-live="polite" aria-atomic="true">{{active+1}} / {{items.length}}</span>
   <button class="announcement-arrow" aria-label="下一条公告" :disabled="active===items.length-1" @click="step(1)"><Icon name="ChevronRight" :size="16"/></button>
  </div>
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
</template>
<style scoped>
.login-announcements{margin-top:30px;border:1px solid #ded8cb;border-radius:6px;background:#fffaf2;min-width:0;overflow:hidden}
.login-announcements>header{display:flex;align-items:center;justify-content:space-between;gap:10px;padding:13px 16px;border-bottom:1px solid #eae2d4}
.login-announcements>header>span{display:flex;align-items:center;gap:7px;color:#795d35;font-size:12px;font-weight:600}
.login-announcement-card{flex:0 0 100%;width:100%;min-width:0;box-sizing:border-box;scroll-snap-align:start;scroll-snap-stop:always;padding:16px;max-height:220px;overflow-y:auto;overscroll-behavior-y:contain}
.login-announcement-card .bulletin-meta{color:#8b7655;font-size:10px;flex-wrap:wrap;gap:6px}
.login-announcement-card .bulletin-meta time{font-size:9px}
.login-announcement-card h3{font-size:16px;line-height:1.6;color:#4d493c;margin:11px 0 7px;overflow-wrap:anywhere}
.login-announcement-card p{font-size:12px;line-height:1.9;color:#6c685c;white-space:pre-wrap;overflow-wrap:anywhere;margin:0}
.login-announcement-empty{padding:16px;color:#918572;font-size:12px;display:flex;justify-content:space-between;gap:10px}
.login-announcement-card:focus-visible{outline:2px solid #b27642;outline-offset:-3px}
.announcement-carousel{flex:1;min-width:0}
.announcement-slides{display:flex;overflow-x:auto;scroll-snap-type:x mandatory;scrollbar-width:none;-ms-overflow-style:none;overscroll-behavior-x:contain}
.announcement-slides::-webkit-scrollbar{display:none}
.announcement-slides .announcement-preview{flex:0 0 100%;width:100%;box-sizing:border-box;min-height:32px;scroll-snap-align:start;scroll-snap-stop:always;padding:3px 2px}
.announcement-preview .badge{flex-shrink:0}.announcement-preview strong{min-width:0}.announcement-preview time{flex-shrink:0}
.announcement-controls{display:flex;align-items:center;gap:6px;flex-shrink:0}
.announcement-arrow{display:grid;place-items:center;width:28px;height:28px;border:1px solid #d5cbb6;border-radius:5px;color:#725b37;background:#ffffff70}
.announcement-arrow:disabled{opacity:.3;cursor:default}.announcement-arrow:not(:disabled):hover{background:#fff}
.announcement-position{min-width:44px;text-align:center;font-size:10px;color:#847250;font-variant-numeric:tabular-nums}
@media(max-width:640px){.announcement-strip{flex-wrap:wrap}.announcement-carousel{order:3;flex-basis:100%}.announcement-controls{margin-left:auto}.announcement-strip>.text-button{margin-left:0}}
</style>

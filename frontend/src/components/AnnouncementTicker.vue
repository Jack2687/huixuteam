<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import type { Announcement } from '../lib/api';
import Icon from './Icon.vue';
defineProps<{items:Announcement[]}>();
const emit=defineEmits<{open:[item:Announcement]}>();
const viewport=ref<HTMLElement>(),group=ref<HTMLElement>();
const width=ref(0),distance=ref(0),paused=ref(false),reducedMotion=ref(false);
const style=computed(()=>({'--ticker-distance':`${distance.value}px`,'--ticker-duration':`${Math.max(distance.value/36,1)}s`,'--ticker-min-width':`${width.value}px`}));
let observer:ResizeObserver|undefined,motion:MediaQueryList|undefined;
function measure(){
 width.value=viewport.value?.clientWidth||0;
 distance.value=group.value?.getBoundingClientRect().width||0;
}
function updateMotion(){reducedMotion.value=motion?.matches||false}
onMounted(()=>{
 motion=window.matchMedia('(prefers-reduced-motion: reduce)');updateMotion();motion.addEventListener('change',updateMotion);
 observer=new ResizeObserver(measure);
 if(viewport.value)observer.observe(viewport.value);
 if(group.value)observer.observe(group.value);
 measure();
});
onUnmounted(()=>{observer?.disconnect();motion?.removeEventListener('change',updateMotion)});
</script>
<template>
 <div class="announcement-ticker" :class="{'is-paused':paused,'is-reduced':reducedMotion,'is-ready':distance>0}" :style="style">
  <div ref="viewport" class="ticker-viewport" role="group" aria-label="公告向左滚动，点击查看详情">
   <div class="ticker-track">
    <div ref="group" class="ticker-group">
     <button v-for="item in items" :key="item.id" type="button" class="ticker-item" @click="emit('open',item)"><span v-if="item.pinned" class="badge amber">置顶</span><strong>{{item.title}}</strong><span class="ticker-content">{{item.content}}</span><span class="ticker-separator" aria-hidden="true">·</span></button>
    </div>
    <div class="ticker-group ticker-copy" aria-hidden="true">
     <span v-for="item in items" :key="item.id" class="ticker-item" @click="emit('open',item)"><span v-if="item.pinned" class="badge amber">置顶</span><strong>{{item.title}}</strong><span class="ticker-content">{{item.content}}</span><span class="ticker-separator">·</span></span>
    </div>
   </div>
  </div>
  <button v-if="!reducedMotion" type="button" class="ticker-toggle" :aria-label="paused?'继续滚动公告':'暂停滚动公告'" :title="paused?'继续滚动':'暂停滚动'" :aria-pressed="paused" @click="paused=!paused"><Icon :name="paused?'Play':'Pause'" :size="14"/></button>
 </div>
</template>
<style scoped>
.announcement-ticker{display:flex;align-items:center;gap:8px;min-width:0;width:100%}
.ticker-viewport{flex:1;min-width:0;overflow:hidden}
.ticker-track{display:flex;width:max-content;animation:announcement-left var(--ticker-duration) linear infinite;animation-play-state:paused}
.is-ready .ticker-track{animation-play-state:running}
.ticker-group{display:flex;align-items:center;flex:none;width:max-content;min-width:var(--ticker-min-width);box-sizing:border-box}
.ticker-item{display:flex;align-items:center;flex:none;gap:10px;min-height:38px;max-width:none;padding:4px 0;text-align:left;font-size:12px;color:#6c685c;white-space:nowrap}
.ticker-item{cursor:pointer}
.ticker-item strong{font-size:12px;color:#654f32;flex:none}
.ticker-content{white-space:nowrap}
.ticker-separator{padding:0 25px;color:#b89d77;font-size:20px}
.ticker-toggle{flex:none;display:grid;place-items:center;width:28px;height:28px;border:1px solid #d5cbb6;border-radius:5px;color:#725b37;background:#ffffff70}
.ticker-toggle:hover{background:#fff}
.ticker-toggle:focus-visible,.ticker-item:focus-visible{outline:2px solid #b27642;outline-offset:-2px}
.is-paused .ticker-track,.ticker-viewport:hover .ticker-track,.ticker-viewport:focus-within .ticker-track{animation-play-state:paused}
.is-reduced .ticker-track{animation:none}
.is-reduced .ticker-copy{display:none}
.is-reduced .ticker-viewport{overflow-x:auto;scrollbar-width:none}
.is-reduced .ticker-viewport::-webkit-scrollbar{display:none}
@keyframes announcement-left{from{transform:translateX(0)}to{transform:translateX(calc(-1 * var(--ticker-distance)))}}
</style>

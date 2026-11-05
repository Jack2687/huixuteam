<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { branding } from '../lib/branding';
const failed=ref(false);
watch(()=>branding.value.icon_url,()=>{failed.value=false});
const src=computed(()=>failed.value?'/brand-default.svg':branding.value.icon_url);
</script>
<template>
  <div class="brand" aria-label="回序 HUIXU">
    <img class="brand-mark" :src="src" alt="" width="40" height="40" @error="failed=true"/>
    <div><strong>回序<span>HUIXU</span></strong><small>服务有序 · 进度有据</small></div>
  </div>
</template>

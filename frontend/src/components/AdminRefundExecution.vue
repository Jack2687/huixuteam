<script setup lang="ts">
import {ref} from 'vue';
import {api} from '../lib/api';
const props=defineProps<{id:string;recovery:{running:boolean;needs_review:boolean;can_release:boolean}}>();
const emit=defineEmits<{changed:[];busy:[value:boolean]}>();
const action=ref('resume'),note=ref(''),confirmed=ref(false),busy=ref(false),error=ref('');
async function submit(){
 busy.value=true;emit('busy',true);error.value='';
 try{await api(`/api/admin/refunds/${props.id}/resume`,{method:'POST',body:JSON.stringify({action:action.value,note:note.value})});note.value='';confirmed.value=false;emit('changed')}
 catch(e){error.value=(e as Error).message}finally{busy.value=false;emit('busy',false)}
}
</script>
<template>
 <form class="refund-recovery" @submit.prevent="submit">
  <h4>人工处置</h4>
  <select v-model="action" aria-label="选择处置方式">
   <option v-if="!recovery.needs_review" value="resume">恢复办理</option>
   <template v-else><option value="resume" disabled>请先核实本次处理结果</option><option value="confirm_accepted">已确认受理，恢复办理</option><option value="confirm_rejected">已确认未受理，重新处理</option></template>
   <option v-if="recovery.can_release" value="fail_cleaned">已核实并清理资源，结束失败并释放费用</option>
  </select>
  <textarea v-model="note" rows="2" required minlength="5" maxlength="300" placeholder="填写核实依据或处理原因，请勿包含敏感资料" aria-label="处理原因"/>
  <label class="checkbox-label"><input v-model="confirmed" type="checkbox" required/>我已核实当前状态，确认执行所选处理</label>
  <p>恢复办理会进入后台队列。结束失败前请确认本次服务资源已经清理。</p>
  <p v-if="error" class="form-error" role="alert">{{error}}</p>
  <button class="button primary" :disabled="busy||!confirmed||recovery.running||(recovery.needs_review&&action==='resume')">执行所选处置</button>
 </form>
</template>

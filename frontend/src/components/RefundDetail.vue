<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, onUnmounted, ref } from "vue";
import Modal from "./Modal.vue";
import Icon from "./Icon.vue";
import Pagination from "./Pagination.vue";
import {
  api,
  date,
  money,
  statuses,
  feeLabel,
  type Refund,
  type RefundEvent,
} from "../lib/api";
const props = defineProps<{ id: string; admin?: boolean }>();
const emit = defineEmits<{ close: []; changed: [] }>();
const AdminRefundExecution=defineAsyncComponent(()=>import('./AdminRefundExecution.vue'));
const data = ref<{ refund: Refund; events: RefundEvent[]; recovery?:{running:boolean;needs_review:boolean;can_release:boolean} }>(),
  error = ref(""),
  busy = ref(false),
  confirmCancel = ref(false),
  page = ref(1),
  size = ref(20);
const handling=ref(false);
let timer:ReturnType<typeof setInterval>;
const events = computed(
  () =>
    data.value?.events.slice(
      (page.value - 1) * size.value,
      page.value * size.value,
    ) || [],
);
async function load() {
  busy.value = true;
  error.value = "";
  try {
    data.value = await api(
      `/api/${props.admin ? "admin/" : ""}refunds/${props.id}`,
    );
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
async function cancel() {
  busy.value = true;
  try {
    await api(`/api/refunds/${props.id}/cancel`, { method: "POST" });
    confirmCancel.value = false;
    await load();
    emit("changed");
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
onMounted(()=>{load();timer=setInterval(()=>{if(!document.hidden&&!busy.value&&!handling.value&&['queued','processing','verifying'].includes(data.value?.refund.status||''))load()},15000)});
onUnmounted(()=>clearInterval(timer));
</script>
<template>
  <Modal
    title="服务单详情"
    :description="`#${id.toUpperCase()}`"
    :busy="busy"
    @close="!busy && emit('close')"
    ><div v-if="data" class="detail-content">
      <div class="detail-summary">
        <div>
          <small>服务账号</small>
          <h3>{{ data.refund.email }}</h3>
        </div>
        <span class="badge" :class="statuses[data.refund.status]?.color"
          ><i />{{ statuses[data.refund.status]?.label }}</span
        >
      </div>
      <div class="info-grid">
        <div>
          <small>服务费</small><strong>{{ money(data.refund.fee) }} 分</strong>
        </div>
        <div>
          <small>结算状态</small
          ><strong>{{ feeLabel[data.refund.fee_status] }}</strong>
        </div>
        <div>
          <small>收件时间</small
          ><strong>{{ date(data.refund.created_at) }}</strong>
        </div>
      </div>
      <div class="notice">
        <Icon name="Clock3" :size="17" /><span>{{ data.refund.message }}</span>
      </div>
      <h4>服务动态</h4>
      <div class="timeline">
        <div v-for="event in events" :key="event.id">
          <i class="timeline-dot" />
          <div>
            <strong>{{ event.message }}</strong
            ><small>{{ date(event.created_at) }}</small>
          </div>
        </div>
      </div>
      <Pagination
        v-model:page="page"
        v-model:size="size"
        :total="data.events.length"
        label="服务动态分页"
      />
      <AdminRefundExecution v-if="admin&&data.recovery" :id="id" :recovery="data.recovery" @changed="load();emit('changed')" @busy="handling=$event"/>
      <div v-if="confirmCancel" class="notice">
        <span
          >撤回这笔申请后，预留的
          {{ money(data.refund.fee) }} 分会恢复为可用积分。</span
        ><button class="button secondary" :disabled="busy" @click="cancel">
          确认撤回
        </button>
      </div>
    </div>
    <div v-else-if="busy" class="loading">
      <Icon name="LoaderCircle" class="spin" />正在读取申请
    </div>
    <p v-if="error" class="form-error" role="alert">{{ error }}</p>
    <footer class="modal-actions">
      <button
        v-if="!admin && data?.refund.status === 'queued'"
        class="button secondary danger"
        :disabled="busy"
        @click="confirmCancel = !confirmCancel"
      >
        撤回申请</button
      ><button class="button secondary" :disabled="busy" @click="load">
        <Icon name="RefreshCw" :class="{ spin: busy }" />读取最新动态
      </button>
    </footer></Modal
  >
</template>

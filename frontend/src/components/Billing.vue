<script setup lang="ts">
import { onUnmounted, ref, watch } from "vue";
import Icon from "./Icon.vue";
import Pagination from "./Pagination.vue";
import Redeem from "./Redeem.vue";
import { api, date, money, type User, type Entry, type Page } from "../lib/api";
const props = defineProps<{ user: User; refresh: number }>();
const emit = defineEmits<{
  detail: [id: string];
  redeemed: [message: string];
}>();
const items = ref<Entry[]>([]),
  total = ref(0),
  page = ref(1),
  size = ref(50),
  error = ref(""),
  loading = ref(true);
const kinds: Record<string, { label: string; icon: string; color: string }> = {
  redeem: { label: "兑换入账", icon: "KeyRound", color: "green" },
  credit: { label: "人工入账", icon: "Plus", color: "green" },
  debit: { label: "人工扣减", icon: "Minus", color: "red" },
  freeze: { label: "服务费预留", icon: "LockKeyhole", color: "amber" },
  release: { label: "预留解除", icon: "RotateCcw", color: "blue" },
  settlement: { label: "服务完成扣费", icon: "CreditCard", color: "gray" },
};
let controller: AbortController | undefined;
async function load() {
  controller?.abort();
  const c = new AbortController();
  controller = c;
  error.value = "";
  loading.value = true;
  try {
    const data = await api<Page<Entry>>(
      `/api/billing?page=${page.value}&size=${size.value}`,
      { signal: c.signal },
    );
    items.value = data.items;
    total.value = data.total;
  } catch (e) {
    if ((e as Error).name !== "AbortError") error.value = (e as Error).message;
  } finally {
    if (c === controller) loading.value = false;
  }
}
watch(() => [page.value, size.value, props.refresh], load, { immediate: true });
onUnmounted(() => controller?.abort());
</script>
<template>
 <div class="billing-overview">
  <div class="metric-grid three">
    <section class="metric-card">
      <span>可支配积分</span
      ><strong>{{ money(user.balance - user.frozen) }} 分</strong>
    </section>
    <section class="metric-card">
      <span>服务预留</span><strong>{{ money(user.frozen) }} 分</strong>
    </section>
    <section class="metric-card">
      <span>单笔服务价格</span><strong>{{ money(user.service_fee) }} 分</strong>
    </section>
  </div>
  <Redeem @redeemed="emit('redeemed', $event)" />
 </div>
  <div class="notice">
    <Icon name="CreditCard" :size="19" />
    <div>
      需要补充积分时，请联系服务人员办理。<small
        >提交时预留费用，服务成功后扣除；明确失败或主动撤回后解除预留。</small
      >
    </div>
  </div>
  <section class="table-card">
    <div class="section-title">
      <div>
        <h2>收支记录</h2>
        <p>金额单位为积分，每 1 积分对应 1 R。</p>
      </div>
    </div>
    <div v-if="error" class="error-banner" role="alert">
      {{ error }}<button @click="load">重试</button>
    </div>
    <div class="table-scroll">
      <table>
        <thead>
          <tr>
            <th>变动项目</th>
            <th>发生积分</th>
            <th>变动后总积分</th>
            <th>对应服务单</th>
            <th>时间</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="entry in items" :key="entry.id">
            <td :title="entry.note">
              <span class="ledger-icon" :class="kinds[entry.kind]?.color"
                ><Icon :name="kinds[entry.kind]?.icon" :size="13" /></span
              >{{ kinds[entry.kind]?.label }}
            </td>
            <td
              class="tabular"
              :class="{
                'credit-text':
                  entry.kind === 'credit' || entry.kind === 'redeem',
              }"
            >
              {{ money(entry.amount) }} 分
            </td>
            <td class="tabular">{{ money(entry.balance_after) }} 分</td>
            <td>
              <button
                v-if="entry.refund_id"
                class="text-button"
                @click="emit('detail', entry.refund_id)"
              >
                #{{ entry.refund_id.slice(0, 10) }}</button
              ><span v-else class="muted">—</span>
            </td>
            <td class="muted">{{ date(entry.created_at) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-if="!items.length" class="empty">
      <span><Icon name="Wallet" :size="26" /></span>
      <h3>{{ loading ? "正在加载收支记录" : "暂无收支记录" }}</h3>
      <p>入账、预留、解冻与结算均保留逐笔记录。</p>
    </div>
    <Pagination
      v-model:page="page"
      v-model:size="size"
      :total="total"
      label="收支记录分页"
    />
    <div class="table-footer">
      <span><Icon name="ShieldCheck" :size="13" />预留与解冻只改变可用部分</span>
    </div>
  </section>
</template>

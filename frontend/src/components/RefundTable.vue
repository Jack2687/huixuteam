<script setup lang="ts">
import { onUnmounted, ref, watch } from "vue";
import Icon from "./Icon.vue";
import Pagination from "./Pagination.vue";
import {
  api,
  date,
  money,
  statuses,
  exportCSV,
  type Page,
  type Refund,
} from "../lib/api";
const props = defineProps<{
  overview?: boolean;
  search: string;
  refresh: number;
}>();
const emit = defineEmits<{
  detail: [id: string];
  all: [];
  create: [];
  toast: [message: string];
}>();
const items = ref<Refund[]>([]),
  total = ref(0),
  page = ref(1),
  size = ref(props.overview ? 5 : 20),
  status = ref(""),
  busy = ref(false),
  error = ref("");
const tabs = [
  { value: "", label: "全部" },
  { value: "active", label: "处理中" },
  { value: "succeeded", label: "已完成" },
  { value: "attention", label: "待跟进" },
];
let controller: AbortController | undefined;
let sequence = 0;
async function load() {
  controller?.abort();
  controller = new AbortController();
  const request = ++sequence;
  busy.value = true;
  error.value = "";
  try {
    const data = await api<Page<Refund>>(
      `/api/refunds?${new URLSearchParams({ page: String(page.value), size: String(size.value), status: status.value, q: props.search })}`,
      { signal: controller.signal },
    );
    if (request === sequence) {
      items.value = data.items;
      total.value = data.total;
      if (page.value > Math.max(1, Math.ceil(total.value / size.value)))
        page.value = Math.max(1, Math.ceil(total.value / size.value));
    }
  } catch (e) {
    if ((e as Error).name !== "AbortError" && request === sequence)
      error.value = (e as Error).message;
  } finally {
    if (request === sequence) busy.value = false;
  }
}
watch(
  () => [props.search, status.value],
  () => {
    page.value = 1;
    items.value = [];
    load();
  },
);
watch(() => [page.value, size.value, props.refresh], load, { immediate: true });
onUnmounted(() => controller?.abort());
function download() {
  exportCSV([
    [
      "服务账号",
      "申请编号",
      "办理状态",
      "服务费（分）",
      "收件时间",
      "结算状态",
    ],
    ...items.value.map((r) => [
      r.email,
      r.id,
      statuses[r.status]?.label || r.status,
      String(r.fee / 100),
      date(r.created_at),
      r.fee_status,
    ]),
  ]);
  emit("toast", "本页记录已下载");
}
</script>
<template>
  <section class="table-card">
    <div class="section-title">
      <div>
        <h2>{{ overview ? "最近服务记录" : "申请清单" }}</h2>
        <p>
          {{
            overview
              ? "查看近期提交与当前处理结果。"
              : `${total} 笔记录 · 可检索邮箱与服务状态`
          }}
        </p>
      </div>
      <button v-if="overview" class="text-button" @click="emit('all')">
        展开记录<Icon name="ChevronRight" :size="14" /></button
      ><button
        v-else
        class="button secondary"
        :disabled="!items.length || busy"
        @click="download"
      >
        <Icon name="Download" />下载本页记录
      </button>
    </div>
    <div class="table-toolbar">
      <div class="segmented">
        <button
          v-for="tab in tabs"
          :key="tab.value"
          :class="{ selected: status === tab.value }"
          @click="status = tab.value"
        >
          {{ tab.label }}
        </button>
      </div>
      <label class="filter"
        ><Icon name="ListFilter" :size="14" /><select
          :value="status === 'active' ? '' : status"
          aria-label="按服务状态筛选"
          @change="status = ($event.target as HTMLSelectElement).value"
        >
          <option value="">不限状态</option>
          <option v-for="(s, key) in statuses" :key="key" :value="key">
            {{ s.label }}
          </option>
        </select></label
      >
    </div>
    <div v-if="error" class="error-banner" role="alert">
      <span>{{ error }}</span
      ><button @click="load">重新加载</button>
    </div>
    <div v-if="busy && !items.length" class="loading">
      <Icon name="LoaderCircle" class="spin" />正在读取申请
    </div>
    <template v-else
      ><div class="table-scroll">
        <table>
          <thead>
            <tr>
              <th>服务账号</th>
              <th>办理状态</th>
              <th>服务费</th>
              <th>收件时间</th>
              <th>当前进展</th>
              <th />
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="row in items"
              :key="row.id"
              tabindex="0"
              class="refund-row"
              @click="emit('detail', row.id)"
              @keydown.enter="emit('detail', row.id)"
            >
              <td>
                <div class="account-cell">
                  <span class="account-avatar">{{
                    row.email[0]?.toUpperCase()
                  }}</span>
                  <div>
                    <strong>{{ row.email }}</strong
                    ><small>#{{ row.id.slice(0, 10).toUpperCase() }}</small>
                  </div>
                </div>
              </td>
              <td>
                <span class="badge" :class="statuses[row.status]?.color"
                  ><i />{{ statuses[row.status]?.label }}</span
                >
              </td>
              <td class="tabular">{{ money(row.fee) }} 分</td>
              <td class="muted tabular">{{ date(row.created_at) }}</td>
              <td class="muted">
                {{
                  row.status === "queued"
                    ? "排队等待自动处理"
                    : row.error_code === "password_verification_failed"
                      ? "密码验证失败（401）"
                      : statuses[row.status]?.label
                }}
              </td>
              <td>
                <button
                  class="icon-button"
                  :aria-label="`查看 ${row.email} 的申请`"
                  @click.stop="emit('detail', row.id)"
                >
                  <Icon name="ChevronRight" :size="16" />
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-if="!items.length" class="empty">
        <span><Icon name="Inbox" :size="28" /></span>
        <h3>
          {{ search || status ? "当前条件下没有记录" : "服务记录暂为空" }}
        </h3>
        <p>
          {{
            search || status
              ? "调整关键词或状态后再查看。"
              : "提交第一笔申请后，可在这里持续查看办理进展。"
          }}
        </p>
        <button
          v-if="!search && !status"
          class="button primary"
          @click="emit('create')"
        >
          <Icon name="Plus" />提交申请
        </button>
      </div></template
    ><Pagination
      v-model:page="page"
      v-model:size="size"
      :total="total"
      :sizes="overview ? [5, 20, 50, 100] : [20, 50, 100]"
      label="退款申请分页"
    />
    <div class="table-footer">
      <span><Icon name="ShieldCheck" :size="13" />敏感资料已加密</span
      ><span>15 秒更新一次</span>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, defineAsyncComponent, onUnmounted, ref, watch } from "vue";
import Icon from "./Icon.vue";
import AdminAnnouncements from "./AdminAnnouncements.vue";
import AdminAccountReports from "./AdminAccountReports.vue";
import Modal from "./Modal.vue";
import Pagination from "./Pagination.vue";
import RefundDetail from "./RefundDetail.vue";
const RefundExecutionSettings=defineAsyncComponent(()=>import("./RefundExecutionSettings.vue"));
import {
  api,
  money,
  date,
  copy,
  exportTXT,
  statuses,
  type User,
  type Refund,
  type Page,
} from "../lib/api";
type Code = {
  id: string;
  prefix: string;
  amount: number;
  max_uses: number;
  used_count: number;
  expires_at: string | null;
  disabled: boolean;
  note: string;
  created_at: string;
  exportable: boolean;
};
type Audit = {
  id: number;
  actor: string;
  action: string;
  target: string;
  metadata: Record<string, unknown>;
  created_at: string;
};
type Redemption = {
  id: number;
  prefix: string;
  email: string;
  amount: number;
  created_at: string;
};
type ManagedRefund = Refund & { user_name: string; user_email: string };
const props = defineProps<{ user: User }>();
const emit = defineEmits<{ toast: [message: string]; changed: [] }>();
const can = (permission: string) => props.user.permissions.includes(permission);
const allTabs = [
  { key: "overview", label: "运营概况", permission: "admin.overview.read" },
  { key: "reports", label: "账户报表", permission: "admin.reports.read" },
  { key: "users", label: "账户管理", permission: "admin.users.manage" },
  { key: "refunds", label: "申请清单", permission: "admin.refunds.read" },
  { key: "execution", label: "服务资源与调度", permission: "admin.refund.execute" },
  { key: "announcements", label: "公告发布", permission: "admin.announcements.manage" },
  { key: "codes", label: "兑换码", permission: "admin.codes.manage" },
  { key: "redemptions", label: "入账记录", permission: "admin.codes.manage" },
  { key: "audit", label: "操作留痕", permission: "admin.audit.read" },
  { key: "permissions", label: "权限说明", permission: "admin.overview.read" },
];
const tabs = computed(() => allTabs.filter((t) => can(t.permission)));
const tab = ref("overview"),
  page = ref(1),
  size = ref(20),
  total = ref(0),
  search = ref(""),
  status = ref(""),
  loading = ref(false),
  error = ref("");
const overview = ref<Record<string, number | boolean>>({}),
  users = ref<User[]>([]),
  refunds = ref<ManagedRefund[]>([]),
  codes = ref<Code[]>([]),
  redemptions = ref<Redemption[]>([]),
  audits = ref<Audit[]>([]);
const modal = ref(""),
  target = ref<User>(),
  formEmail = ref(""),
  formName = ref(""),
  formRole = ref("member"),
  formPassword = ref(""),
  formFee = ref(30),
  formDisabled = ref(false),
  creditAmount = ref(100),
  creditDirection = ref<1 | -1>(1),
  note = ref(""),
  codeCount = ref(1),
  codeAmount = ref(100),
  codeUses = ref(1),
  codeExpiry = ref(""),
  generated = ref<string[]>([]),
  busy = ref(false),
  formError = ref(""),
  detailID = ref(""),
  confirmCode = ref<Code>();
const selectedCodes = ref(new Map<string, boolean>());
const deleteIDs = ref<string[]>([]);
const allPageSelected = computed(() => codes.value.length > 0 && codes.value.every(c => selectedCodes.value.has(c.id)));
const somePageSelected = computed(() => codes.value.some(c => selectedCodes.value.has(c.id)));
const hasLegacySelection = computed(() => [...selectedCodes.value.values()].some(exportable => !exportable));
const creditDelta = computed(() => creditDirection.value * Math.round(Number(creditAmount.value || 0) * 100));
const creditPreview = computed(() => (target.value?.balance ?? 0) + creditDelta.value);
const invalidDeduction = computed(() => creditDirection.value === -1 && creditPreview.value < (target.value?.frozen ?? 0));
const roles: Record<string, string> = {
  admin: "管理员",
  operator: "运营人员",
  member: "普通用户",
};
const actions: Record<string, string> = {
  "announcement.create": "撰写公告", "announcement.update": "调整公告", "announcement.delete": "移除公告",
  "user.create": "新增账户",
  "user.update": "调整账户资料与权限",
  "user.password_reset": "重置密码",
  "balance.credit": "积分入账",
  "balance.debit": "积分扣减",
  "code.generate": "生成兑换码",
  "code.update": "更新兑换码",
  "code.redeem": "兑换入账",
  "code.delete": "批量删除兑换码",
  "code.export": "导出兑换码",
};
const capabilities = [
  ["查看逐人资金与申请报表", true, false, false],
  ["编辑、发布与撤下平台公告", true, false, false],
  ["个人申请、账单和 API 密钥", true, true, true],
  ["使用兑换码获得积分", true, true, true],
  ["查看所有用户退款申请", true, true, false],
  ["用户创建、停用及角色分配", true, false, false],
  ["服务费设置与积分调整", true, false, false],
  ["生成、导出、删除兑换码及使用记录", true, false, false],
  ["查看操作操作留痕", true, false, false],
  ["母号、代理、队列调度与异常续跑", true, false, false],
];
let abort: AbortController | undefined,
  searchTimer: ReturnType<typeof setTimeout>,
  creditKey = crypto.randomUUID(),
  creditBody = "";
async function load() {
  abort?.abort();
  const c = new AbortController();
  abort = c;
  error.value = "";
  loading.value = true;
  try {
    if (tab.value === "permissions" || tab.value === "execution" || tab.value === "announcements" || tab.value === "reports") return;
    const path = `/api/admin/${tab.value}?${new URLSearchParams({ page: String(page.value), size: String(size.value), q: search.value, status: status.value })}`;
    if (tab.value === "overview")
      overview.value = await api(path, { signal: c.signal });
    else {
      const data = await api<Page<never>>(path, { signal: c.signal });
      total.value = data.total;
      switch (tab.value) {
        case "users":
          users.value = data.items;
          break;
        case "refunds":
          refunds.value = data.items;
          break;
        case "codes":
          codes.value = data.items;
          for (const code of codes.value) {
            if (selectedCodes.value.has(code.id)) selectedCodes.value.set(code.id, code.exportable);
          }
          break;
        case "redemptions":
          redemptions.value = data.items;
          break;
        case "audit":
          audits.value = data.items;
          break;
      }
    }
  } catch (e) {
    if ((e as Error).name !== "AbortError") error.value = (e as Error).message;
  } finally {
    if (abort === c) loading.value = false;
  }
}
watch(tab, () => {
  selectedCodes.value.clear();
  page.value = 1;
  search.value = "";
  status.value = "";
  total.value = 0;
  load();
});
watch(() => [page.value, size.value, status.value], load);
watch(search, () => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(() => {
    page.value = 1;
    load();
  }, 250);
});
watch(
  () => props.user.role,
  () => {
    if (!tabs.value.some((t) => t.key === tab.value)) tab.value = "overview";
    load();
  },
);
watch(() => props.user.id, load, { immediate: true });
onUnmounted(() => {
  abort?.abort();
  clearTimeout(searchTimer);
});
function open(mode: string, user?: User) {
  target.value = user;
  modal.value = mode;
  formError.value = "";
  formEmail.value = user?.email || "";
  formName.value = user?.name || "";
  formRole.value = user?.role || "member";
  formPassword.value = "";
  formFee.value = (user?.service_fee ?? 3000) / 100;
  formDisabled.value = user?.disabled || false;
  note.value = "";
  creditDirection.value = 1;
  creditAmount.value = 100;
  generated.value = [];
  creditKey = crypto.randomUUID();
  creditBody = "";
}
function close() {
  if (busy.value) return;
  modal.value = "";
  generated.value = [];
  formPassword.value = "";
  formError.value = "";
}
async function save() {
  busy.value = true;
  formError.value = "";
  try {
    if (modal.value === "user") {
      const body = {
        name: formName.value,
        role: formRole.value,
        service_fee: Math.round(Number(formFee.value) * 100),
        disabled: formDisabled.value,
      };
      if (target.value)
        await api(`/api/admin/users/${target.value.id}`, {
          method: "PATCH",
          body: JSON.stringify(body),
        });
      else
        await api("/api/admin/users", {
          method: "POST",
          body: JSON.stringify({
            ...body,
            email: formEmail.value,
            password: formPassword.value,
          }),
        });
    }
    if (modal.value === "password")
      await api(`/api/admin/users/${target.value?.id}/password`, {
        method: "POST",
        body: JSON.stringify({ password: formPassword.value }),
      });
    if (modal.value === "credit") {
      if (invalidDeduction.value) throw new Error("减少积分不能超过当前可用积分");
      const body = JSON.stringify({
        amount: creditDelta.value,
        note: note.value,
      });
      if (creditBody !== body) {
        creditKey = crypto.randomUUID();
        creditBody = body;
      }
      await api(`/api/admin/users/${target.value?.id}/credit`, {
        method: "POST",
        headers: { "Idempotency-Key": creditKey },
        body,
      });
    }
    if (modal.value === "codes") {
      const data = await api<{ codes: string[] }>("/api/admin/codes", {
        method: "POST",
        body: JSON.stringify({
          count: Number(codeCount.value),
          amount: Math.round(Number(codeAmount.value) * 100),
          max_uses: Number(codeUses.value),
          expires_at: codeExpiry.value
            ? new Date(codeExpiry.value).toISOString()
            : null,
          note: note.value,
        }),
      });
      generated.value = data.codes;
      await load();
      emit("toast", "兑换码已生成，请复制或导出保存");
      return;
    }
    modal.value = "";
    formPassword.value = "";
    await load();
    emit("changed");
    emit("toast", "已保存，操作已记录到操作留痕");
  } catch (e) {
    formError.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
async function toggleCode() {
  if (!confirmCode.value) return;
  busy.value = true;
  formError.value = "";
  try {
    await api(`/api/admin/codes/${confirmCode.value.id}`, {
      method: "PATCH",
      body: JSON.stringify({ disabled: !confirmCode.value.disabled }),
    });
    confirmCode.value = undefined;
    await load();
    emit("toast", "兑换码状态已更新");
  } catch (e) {
    formError.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
async function copyCodes() {
  try {
    await copy(generated.value.join("\n"));
    emit("toast", "兑换码已复制");
  } catch (e) {
    formError.value = (e as Error).message;
  }
}
function downloadCodes() {
  exportTXT(generated.value, "回序-积分兑换码.txt");
}
function selectCode(code: Code, checked: boolean) {
  if (!checked) selectedCodes.value.delete(code.id);
  else if (selectedCodes.value.size < 1000) selectedCodes.value.set(code.id, code.exportable);
  else emit("toast", "每批最多选择 1000 个兑换码");
}
function selectPage(checked: boolean) {
  for (const code of codes.value) selectCode(code, checked);
}
async function exportSelectedCodes() {
  if (!selectedCodes.value.size || busy.value) return;
  busy.value = true;
  error.value = "";
  try {
    const data = await api<{ codes: string[] }>("/api/admin/codes/export", {
      method: "POST", body: JSON.stringify({ ids: [...selectedCodes.value.keys()] }),
    });
    exportTXT(data.codes, "回序-积分兑换码.txt");
    emit("toast", `已导出 ${data.codes.length} 个兑换码`);
  } catch (e) { error.value = (e as Error).message; }
  finally { busy.value = false; }
}
async function deleteSelectedCodes() {
  if (!deleteIDs.value.length || busy.value) return;
  busy.value = true;
  formError.value = "";
  try {
    const ids = [...deleteIDs.value];
    await api("/api/admin/codes/delete", { method: "POST", body: JSON.stringify({ ids }) });
    ids.forEach(id => selectedCodes.value.delete(id));
    deleteIDs.value = [];
    page.value = Math.min(page.value, Math.max(1, Math.ceil((total.value - ids.length) / size.value)));
    await load();
    emit("toast", `已删除 ${ids.length} 个兑换码，历史兑换记录保留`);
  } catch (e) { formError.value = (e as Error).message; }
  finally { busy.value = false; }
}
function codeState(code: Code) {
  return code.disabled
    ? "已停用"
    : code.used_count >= code.max_uses
      ? "已用完"
      : code.expires_at && new Date(code.expires_at).getTime() <= Date.now()
        ? "已过期"
        : "可兑换";
}
</script>
<template>
 <div class="admin-layout">
  <div class="admin-tabs" role="tablist" aria-label="运营控制台功能" aria-orientation="vertical">
    <button
      v-for="item in tabs"
      :key="item.key"
      role="tab"
      :aria-selected="tab === item.key"
      :class="{ selected: tab === item.key }"
      @click="tab = item.key"
    >
      {{ item.label }}
    </button>
  </div>
  <div class="admin-panel">
  <div v-if="error" class="error-banner" role="alert">
    {{ error }}<button @click="load">重试</button>
  </div>
  <template v-if="tab === 'overview'"
    ><div class="metric-grid">
      <section
        v-for="metric in [
          { key: 'users', title: '账户数量' },
          { key: 'refunds', title: '申请清单' },
          { key: 'active', title: '尚在办理' },
          { key: 'succeeded', title: '服务完成数' },
        ]"
        :key="metric.key"
        class="metric-card"
      >
        <div>
          {{ metric.title
          }}<span class="metric-icon"><Icon name="FileText" :size="16" /></span>
        </div>
        <strong>{{ overview[metric.key] || 0 }}</strong>
        <p>全站账户合计</p>
      </section>
    </div>
    <div v-if="user.role === 'admin'" class="metric-grid three">
      <section
        v-for="metric in [
          { key: 'balance', title: '账户积分总额' },
          { key: 'frozen', title: '已预留积分' },
          { key: 'revenue', title: '累计服务收入' },
        ]"
        :key="metric.key"
        class="metric-card"
      >
        <span>{{ metric.title }}</span
        ><strong>{{ money(Number(overview[metric.key] || 0)) }} 分</strong>
      </section>
    </div>
    <div class="notice">
      <Icon name="CircleAlert" />
      <div>
        {{overview.provider_ready ? '服务队列正在运行' : '服务队列尚未开启'}}<small
          >费用按办理结果结算。需要协助的申请请核查后处理，队列规则可在服务资源中配置。</small
        >
      </div>
    </div>
    <section class="doc-notes">
      <h3>运营指引</h3>
      <p>
        当前角色：{{
          roles[user.role]
        }}。管理接口逐次检查角色与账号状态。用户角色、服务费、停用状态、积分调整和兑换码操作均有审计记录。
      </p>
      <div>
        <span>01</span>
        <section>
          <h4>配置账户与访问范围</h4>
          <p>为成员建立独立账户，再按职责授予相应角色。</p>
        </section>
      </div>
      <div>
        <span>02</span>
        <section>
          <h4>准备积分兑换码</h4>
          <p>设置面额、使用次数和截止时间，成员即可在资金页面兑换。</p>
        </section>
      </div>
      <div>
        <span>03</span>
        <section>
          <h4>核查服务与管理记录</h4>
          <p>从服务记录了解申请进展，再对照操作留痕核查后台变更。</p>
        </section>
      </div>
    </section></template
  >
  <AdminAnnouncements v-else-if="tab === 'announcements'" @toast="emit('toast',$event)" @changed="emit('changed')"/>
  <AdminAccountReports v-else-if="tab === 'reports'"/>
  <RefundExecutionSettings v-else-if="tab === 'execution'" @toast="emit('toast',$event)"/>
  <section v-else-if="tab === 'permissions'" class="table-card">
    <div class="section-title">
      <div>
        <h2>角色与权限</h2>
        <p>提供三类角色，可在账户管理中分配；访问范围由服务端校验。</p>
      </div>
    </div>
    <div class="table-scroll">
      <table>
        <thead>
          <tr>
            <th>功能权限</th>
            <th>管理员</th>
            <th>运营人员</th>
            <th>普通用户</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in capabilities" :key="String(row[0])">
            <td>{{ row[0] }}</td>
            <td v-for="(allowed, index) in row.slice(1)" :key="index">
              <span class="badge" :class="allowed ? 'green' : 'gray'">{{
                allowed ? "允许" : "无权限"
              }}</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div class="table-footer">
      <span
        ><Icon name="ShieldCheck" :size="14" />普通用户仅可访问本人的记录；API
        密钥不授予后台管理权限。</span
      >
    </div>
  </section>
  <section v-else class="table-card">
    <div class="section-title">
      <div>
        <h2>{{ tabs.find((t) => t.key === tab)?.label }}</h2>
        <p>
          {{
            tab === "codes"
              ? `共 ${total} 个兑换码，勾选后可批量删除或导出 TXT。`
              : `共 ${total} 条记录`
          }}
        </p>
      </div>
      <div class="heading-actions">
        <button
          class="button secondary"
          :disabled="loading"
          aria-label="刷新管理记录"
          @click="load"
        >
          <Icon name="RefreshCw" :class="{ spin: loading }" />刷新</button
        ><button
          v-if="tab === 'users'"
          class="button primary"
          @click="open('user')"
        >
          <Icon name="Plus" />新增账户</button
        ><button
          v-if="tab === 'codes'"
          class="button primary"
          @click="open('codes')"
        >
          <Icon name="Plus" />生成兑换码
        </button>
      </div>
    </div>
    <div v-if="tab === 'codes'" class="code-batch-toolbar">
      <span>已选 {{ selectedCodes.size }} 个<span class="muted">（可跨页选择）</span></span>
      <button class="text-button" :disabled="!selectedCodes.size || busy" @click="selectedCodes.clear()">清空选择</button>
      <button class="button secondary" :disabled="!selectedCodes.size || busy || hasLegacySelection" @click="exportSelectedCodes"><Icon name="Download" />导出所选 TXT</button>
      <button class="button secondary danger" :disabled="!selectedCodes.size || busy" @click="deleteIDs = [...selectedCodes.keys()]; formError = ''">批量删除</button>
      <p v-if="hasLegacySelection" class="field-help">所选包含未保存完整码的历史兑换码，请取消选择这些历史码后导出。</p>
    </div>
    <div v-if="tab === 'users' || tab === 'refunds'" class="admin-toolbar">
      <input
        v-model="search"
        :aria-label="tab === 'users' ? '搜索用户' : '搜索所有申请'"
        :placeholder="
          tab === 'users' ? '搜索姓名或邮箱…' : '搜索服务账号、用户邮箱或编号…'
        "
      /><select
        v-if="tab === 'refunds'"
        v-model="status"
        aria-label="管理办理状态"
      >
        <option value="">不限状态</option>
        <option v-for="(state, key) in statuses" :key="key" :value="key">
          {{ state.label }}
        </option>
      </select>
    </div>
    <div class="table-scroll">
      <table v-if="tab === 'users'">
        <thead>
          <tr>
            <th>用户</th>
            <th>角色</th>
            <th>可用 / 已预留积分</th>
            <th>服务单价</th>
            <th>状态</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="u in users" :key="u.id">
            <td>
              <div class="account-cell">
                <span class="account-avatar">{{ u.name[0] }}</span>
                <div>
                  <strong>{{ u.name }}</strong
                  ><small>{{ u.email }}</small>
                </div>
              </div>
            </td>
            <td>
              <span class="badge gray">{{ roles[u.role] }}</span>
            </td>
            <td>{{ money(u.balance - u.frozen) }} / {{ money(u.frozen) }}</td>
            <td>{{ money(u.service_fee) }} 分</td>
            <td>
              <span class="badge" :class="u.disabled ? 'red' : 'green'">{{
                u.disabled ? "已停用" : "正常"
              }}</span>
            </td>
            <td>
              <div class="admin-row-actions">
                <button class="text-button" @click="open('user', u)">
                  编辑权限</button
                ><button class="text-button" @click="open('credit', u)">
                  调整积分</button
                ><button class="text-button" @click="open('password', u)">
                  重置密码
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
      <table v-if="tab === 'refunds'">
        <thead>
          <tr>
            <th>服务账号</th>
            <th>所属用户</th>
            <th>状态</th>
            <th>服务费</th>
            <th>收件时间</th>
            <th />
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in refunds" :key="r.id">
            <td>{{ r.email }}</td>
            <td>
              <div class="account-cell">
                <div>
                  <strong>{{ r.user_name }}</strong
                  ><small>{{ r.user_email }}</small>
                </div>
              </div>
            </td>
            <td>
              <span class="badge" :class="statuses[r.status]?.color">{{
                statuses[r.status]?.label
              }}</span>
            </td>
            <td>{{ money(r.fee) }} 分</td>
            <td class="muted">{{ date(r.created_at) }}</td>
            <td>
              <button class="text-button" @click="detailID = r.id">
                查看详情<Icon name="ChevronRight" :size="13" />
              </button>
            </td>
          </tr>
        </tbody>
      </table>
      <table v-if="tab === 'codes'">
        <thead>
          <tr>
            <th class="selection-cell"><input type="checkbox" aria-label="选择本页兑换码" :checked="allPageSelected" :indeterminate="somePageSelected && !allPageSelected" :disabled="busy || !codes.length" @change="selectPage(($event.target as HTMLInputElement).checked)" /></th>
            <th>兑换码</th>
            <th>面额</th>
            <th>已用 / 上限</th>
            <th>有效期</th>
            <th>状态</th>
            <th>备注</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="c in codes" :key="c.id">
            <td class="selection-cell"><input type="checkbox" :aria-label="`选择兑换码 ${c.prefix}`" :checked="selectedCodes.has(c.id)" :disabled="busy" @change="selectCode(c, ($event.target as HTMLInputElement).checked)" /></td>
            <td>
              <code>{{ c.prefix }}••••</code>
              <small v-if="!c.exportable" class="legacy-code-note">历史码不可导出</small>
            </td>
            <td>{{ money(c.amount) }} 分</td>
            <td>{{ c.used_count }} / {{ c.max_uses }}</td>
            <td>{{ c.expires_at ? date(c.expires_at) : "长期有效" }}</td>
            <td>
              <span
                class="badge"
                :class="codeState(c) === '可兑换' ? 'green' : 'gray'"
                >{{ codeState(c) }}</span
              >
            </td>
            <td :title="c.note" class="note-cell">{{ c.note || "—" }}</td>
            <td>
              <button
                class="text-button"
                :class="{ danger: !c.disabled }"
                @click="
                  confirmCode = c;
                  formError = '';
                "
              >
                {{ c.disabled ? "启用" : "停用" }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
      <table v-if="tab === 'redemptions'">
        <thead>
          <tr>
            <th>兑换码</th>
            <th>用户邮箱</th>
            <th>获得积分</th>
            <th>兑换时间</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in redemptions" :key="r.id">
            <td>
              <code>{{ r.prefix }}••••</code>
            </td>
            <td>{{ r.email }}</td>
            <td class="credit-text">+{{ money(r.amount) }} 分</td>
            <td>{{ date(r.created_at) }}</td>
          </tr>
        </tbody>
      </table>
      <table v-if="tab === 'audit'">
        <thead>
          <tr>
            <th>操作</th>
            <th>操作人</th>
            <th>目标</th>
            <th>变更内容</th>
            <th>时间</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="a in audits" :key="a.id">
            <td>{{ actions[a.action] || a.action }}</td>
            <td>{{ a.actor }}</td>
            <td class="ops-reference">{{ a.target.slice(0, 12) }}</td>
            <td>
              <details class="audit-data">
                <summary>查看记录</summary>
                <pre>{{ JSON.stringify(a.metadata, null, 2) }}</pre>
              </details>
            </td>
            <td>{{ date(a.created_at) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-if="!total" class="empty">
      <span
        ><Icon
          :name="loading ? 'LoaderCircle' : 'Inbox'"
          :class="{ spin: loading }"
          :size="25"
      /></span>
      <h3>{{ loading ? "正在加载管理记录" : "暂无记录" }}</h3>
      <p>记录会在对应操作完成后显示。</p>
    </div>
    <Pagination
      v-model:page="page"
      v-model:size="size"
      :total="total"
      label="管理记录分页"
    />
  </section>
 </div></div>
  <Modal
    v-if="modal"
    :title="
      modal === 'user'
        ? target
          ? '编辑用户与权限'
          : '新增账户'
        : modal === 'credit'
          ? '调整积分'
          : modal === 'password'
            ? '重置登录密码'
            : '生成兑换码'
    "
    :description="target?.email"
    :busy="busy"
    @close="close"
    ><form v-if="!generated.length" @submit.prevent="save">
      <div class="modal-body">
        <template v-if="modal === 'user'"
          ><div class="form-grid">
            <label
              >账户称呼<input
                v-model="formName"
                required
                maxlength="25" /></label
            ><label
              >邮箱<input
                v-model="formEmail"
                type="email"
                :disabled="!!target"
                required
                maxlength="254" /></label
            ><label v-if="!target" class="span-two"
              >首次登录密码<input
                v-model="formPassword"
                type="password"
                minlength="12"
                maxlength="72"
                autocomplete="new-password"
                required
                placeholder="至少 12 位" /></label
            ><label
              >角色<select v-model="formRole">
                <option
                  v-for="(label, value) in roles"
                  :key="value"
                  :value="value"
                >
                  {{ label }}
                </option>
              </select></label
            ><label
              >单笔服务价格（积分）<input
                v-model="formFee"
                type="number"
                min="0"
                max="100000"
                step="0.01"
                required
            /></label>
          </div>
          <label class="checkbox-label"
            ><input
              v-model="formDisabled"
              type="checkbox"
              :disabled="target?.id === user.id"
            /><span>停用此账号（登录会话和 API 密钥将无法访问）</span></label
          >
          <p class="field-help">
            调整服务费只影响之后的新申请。角色变更将结束该账号的现有登录会话。
          </p></template
        ><template v-if="modal === 'password'"
          ><label
            >新密码<input
              v-model="formPassword"
              type="password"
              minlength="12"
              maxlength="72"
              required
              autocomplete="new-password"
              placeholder="至少 12 位"
          /></label>
          <p>
            保存后该用户的全部登录会话将立即失效，需要使用新密码重新登录。
          </p></template
        ><template v-if="modal === 'credit'"
          ><div class="credit-direction" role="group" aria-label="积分调整方向">
            <button type="button" class="button" :class="creditDirection === 1 ? 'primary' : 'secondary'" :aria-pressed="creditDirection === 1" :disabled="busy" @click="creditDirection = 1">＋ 增加积分</button>
            <button type="button" class="button" :class="creditDirection === -1 ? 'primary' : 'secondary'" :aria-pressed="creditDirection === -1" :disabled="busy" @click="creditDirection = -1">− 减少积分</button>
          </div><label
            >调整数量（积分）<input
              v-model="creditAmount"
              type="number"
              min="0.01"
              max="1000000"
              step="0.01"
              required /></label
          ><label
            >调整备注<textarea
              v-model="note"
              rows="3"
              required
              maxlength="160"
              placeholder="请填写付款核对或调整依据"
            />
          </label>
          <p class="field-help">当前总积分 {{ money(target?.balance) }}，可用 {{ money((target?.balance ?? 0) - (target?.frozen ?? 0)) }}，已预留 {{ money(target?.frozen) }}。</p>
          <div class="notice">
            本次{{ creditDirection === 1 ? '增加' : '减少' }} {{ money(Math.abs(creditDelta)) }} 分，调整后总积分 {{ money(creditPreview) }} 分。操作会写入收支记录和操作留痕。
          </div><p v-if="invalidDeduction" class="form-error">减少数量超过可用积分，已预留积分不能扣减。</p></template
        ><template v-if="modal === 'codes'"
          ><div class="form-grid">
            <label
              >积分面额<input
                v-model="codeAmount"
                type="number"
                min="0.01"
                max="1000000"
                step="0.01"
                required /></label
            ><label
              >生成数量<input
                v-model="codeCount"
                type="number"
                min="1"
                max="100"
                step="1"
                required /></label
            ><label
              >每个码使用次数<input
                v-model="codeUses"
                type="number"
                min="1"
                max="10000"
                step="1"
                required /></label
            ><label
              >有效期（留空为长期）<input
                v-model="codeExpiry"
                type="datetime-local" /></label
            ><label class="span-two"
              >备注<input
                v-model="note"
                maxlength="60"
                placeholder="例如：新用户积分"
            /></label>
          </div>
          <div class="notice">
            每个账号对同一码只能兑换一次。生成后可复制或导出，也可在列表中勾选后再次导出 TXT。
          </div></template
        >
        <p v-if="formError" class="form-error" role="alert">{{ formError }}</p>
      </div>
      <footer class="modal-actions">
        <button
          type="button"
          class="button secondary"
          :disabled="busy"
          @click="close"
        >
          取消</button
        ><button class="button primary" :disabled="busy || (modal === 'credit' && invalidDeduction)">
          {{ busy ? "保存中…" : modal === "codes" ? "生成兑换码" : modal === "credit" ? "确认调整" : "保存账户" }}
        </button>
      </footer>
    </form>
    <template v-else
      ><div class="modal-body">
        <p>
          已生成 {{ generated.length }} 个兑换码，每个可兑换
          {{ codeAmount }} 分。导出文件为纯文本，每行一个完整兑换码。
        </p>
        <pre class="generated-codes">{{ generated.join("\n") }}</pre>
        <p v-if="formError" class="form-error">{{ formError }}</p>
      </div>
      <footer class="modal-actions">
        <button class="button secondary" @click="copyCodes">
          <Icon name="Copy" />复制全部</button
        ><button class="button secondary" @click="downloadCodes">
          <Icon name="Download" />导出 TXT</button
        ><button class="button primary" @click="close">已安全保存</button>
      </footer></template
    ></Modal
  >
  <Modal
    v-if="confirmCode"
    :title="confirmCode.disabled ? '启用兑换码' : '停用兑换码'"
    :busy="busy"
    @close="!busy && (confirmCode = undefined)"
    ><div class="modal-body">
      <p>
        {{
          confirmCode.disabled
            ? "启用后，未过期且未用完的兑换码可继续使用。"
            : "停用后，该兑换码将无法继续兑换。已有兑换与积分记录会保留。"
        }}
      </p>
      <p v-if="formError" class="form-error">{{ formError }}</p>
    </div>
    <footer class="modal-actions">
      <button
        class="button secondary"
        :disabled="busy"
        @click="confirmCode = undefined"
      >
        取消</button
      ><button class="button primary" :disabled="busy" @click="toggleCode">
        确认{{ confirmCode.disabled ? "启用" : "停用" }}
      </button>
    </footer></Modal
  >
  <RefundDetail v-if="detailID" :id="detailID" admin @close="detailID = ''" />
  <Modal v-if="deleteIDs.length" title="批量删除兑换码" :busy="busy" @close="!busy && (deleteIDs = [])">
    <div class="modal-body">
      <p>确定删除选中的 {{ deleteIDs.length }} 个兑换码？删除后不能继续兑换、恢复或导出，已有兑换和积分记录会保留。</p>
      <p v-if="formError" class="form-error" role="alert">{{ formError }}</p>
    </div>
    <footer class="modal-actions">
      <button class="button secondary" :disabled="busy" @click="deleteIDs = []">取消</button>
      <button class="button primary" :disabled="busy" @click="deleteSelectedCodes">{{ busy ? '删除中…' : '确认删除' }}</button>
    </footer>
  </Modal>
</template>

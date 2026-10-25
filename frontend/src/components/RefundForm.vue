<script setup lang="ts">
import { computed, ref } from "vue";
import Modal from "./Modal.vue";
import Icon from "./Icon.vue";
import { api, money, type User } from "../lib/api";
import { parseAccounts, type Account } from "../lib/import";
const props = defineProps<{ user: User }>();
const emit = defineEmits<{ close: []; created: [ids: string[]] }>();
const tab = ref("single"),
  email = ref(""),
  password = ref(""),
  totp = ref(""),
  raw = ref(""),
  authorized = ref(false),
  busy = ref(false),
  error = ref(""),
  fileName = ref("");
let idempotency = crypto.randomUUID(),
  lastBody = "";
const parsed = computed(() => {
  try {
    return { accounts: parseAccounts(raw.value), error: "" };
  } catch (e) {
    return { accounts: [] as Account[], error: (e as Error).message };
  }
});
const count = computed(() =>
  tab.value === "single" ? 1 : parsed.value.accounts.length,
);
const ready = computed(
  () =>
    authorized.value &&
    !busy.value &&
    (tab.value === "single"
      ? email.value && password.value && totp.value
      : count.value > 0 && !parsed.value.error),
);
async function file(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0];
  if (!file) return;
  error.value = "";
  if (file.size > 1024 * 1024) {
    error.value = "文件不能超过 1 MB";
    return;
  }
  if (!/\.(txt|csv)$/i.test(file.name)) {
    error.value = "请选择 TXT 或 CSV 文件";
    return;
  }
  fileName.value = file.name;
  raw.value = await file.text();
}
async function submit() {
  if (!ready.value) return;
  busy.value = true;
  error.value = "";
  try {
    const accounts =
      tab.value === "single"
        ? [
            {
              email: email.value.trim(),
              password: password.value,
              totp_secret: totp.value.trim(),
            },
          ]
        : parsed.value.accounts;
    const body = JSON.stringify({ authorized: true, accounts });
    if (body !== lastBody) {
      idempotency = crypto.randomUUID();
      lastBody = body;
    }
    const data = await api<{ ids: string[] }>("/api/refunds", {
      method: "POST",
      headers: { "Idempotency-Key": idempotency },
      body,
    });
    password.value = "";
    totp.value = "";
    raw.value = "";
    lastBody = "";
    emit("created", data.ids);
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <Modal
    title="填写服务申请"
    description="提供以下三项资料，核对费用后提交。"
    wide
    :busy="busy"
    @close="!busy && emit('close')"
    ><form @submit.prevent="submit">
      <div class="modal-body">
        <div class="segmented form-tabs">
          <button
            type="button"
            :class="{ selected: tab === 'single' }"
            @click="tab = 'single'"
          >
            逐个填写</button
          ><button
            type="button"
            :class="{ selected: tab === 'batch' }"
            @click="tab = 'batch'"
          >
            批量填写
          </button>
        </div>
        <div v-if="tab === 'single'" class="form-grid">
          <label class="span-two"
            >服务账号邮箱<input
              v-model="email"
              type="email"
              placeholder="account@example.com"
              required
              autocomplete="off"
              maxlength="254" /></label
          ><label
            >服务账号密码<input
              v-model="password"
              type="password"
              placeholder="输入该账号的密码"
              required
              autocomplete="new-password"
              maxlength="1024" /></label
          ><label
            >验证器密钥（2FA）<input
              v-model="totp"
              type="password"
              placeholder="填写固定密钥，不是六位验证码"
              required
              autocomplete="off"
          /></label>
        </div>
        <template v-else
          ><label class="upload-zone"
            ><Icon name="Upload" :size="25" /><strong>{{
              fileName || "点击选取 TXT / CSV"
            }}</strong
            ><span>一行一份资料，最多 500 行；文件上限 1 MB</span
            ><input
              type="file"
              accept=".txt,.csv"
              aria-label="服务账号文件"
              @change="file" /></label
          ><label
            >也可以直接粘贴资料<textarea
              v-model="raw"
              rows="5"
              class="code-input"
              placeholder="account@example.com---password---2FA_SECRET"
              required
              spellcheck="false"
            />
          </label>
          <p class="field-help">
            支持账号---密码---2FA，分隔符可用 3 个或更多连续横线；也支持 CSV
            和表格三列粘贴。密码以横线开头或结尾时请用
            CSV，含逗号时用双引号包裹。
          </p>
          <p v-if="parsed.error" class="form-error" role="alert">
            {{ parsed.error }}
          </p></template
        >
        <div class="quote-box">
          <div>
            <span>本次账号数</span><strong>{{ count }} 个账号</strong>
          </div>
          <div>
            <span>单笔服务价格</span
            ><strong>{{ money(user.service_fee) }} 分</strong>
          </div>
          <div class="quote-total">
            <span>本次预留积分</span
            ><strong>{{ money(count * user.service_fee) }} 分</strong>
          </div>
          <p>
            服务成功后结算，明确失败后解除预留；同一申请只结算一次。可支配
            {{ money(user.balance - user.frozen) }} 分。
          </p>
        </div>
        <label class="checkbox-label"
          ><input v-model="authorized" type="checkbox" /><span
            >我有权使用以上账号，并授权服务方使用所填资料办理本次退款服务。</span
          ></label
        >
        <div class="privacy-note">
          <Icon
            name="ShieldCheck"
            :size="13"
          />所填凭据加密存储，记录页面不提供密码及密钥查看。
        </div>
        <p class="integration-note">
          费用按服务办理结果结算，不以退款到账为依据。申请尚未开始时可撤回，开始后可在详情跟踪。
        </p>
        <p v-if="error" class="form-error" role="alert">{{ error }}</p>
      </div>
      <footer class="modal-actions">
        <button
          type="button"
          class="button secondary"
          :disabled="busy"
          @click="emit('close')"
        >
          取消</button
        ><button class="button primary" :disabled="!ready">
          <Icon v-if="busy" name="LoaderCircle" class="spin" />{{
            busy ? "正在送出…" : `确认提交 ${count} 笔`
          }}
        </button>
      </footer>
    </form></Modal
  >
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import Icon from "./Icon.vue";
import Modal from "./Modal.vue";
import Pagination from "./Pagination.vue";
import { api, date, copy, type ApiKey } from "../lib/api";
const emit = defineEmits<{ toast: [message: string] }>();
const keys = ref<ApiKey[]>([]),
  error = ref(""),
  showCreate = ref(false),
  keyName = ref(""),
  token = ref(""),
  revoke = ref<ApiKey>(),
  busy = ref(false),
  page = ref(1),
  size = ref(50);
const visible = computed(() =>
  keys.value.slice((page.value - 1) * size.value, page.value * size.value),
);
const endpoint=location.origin+'/open/v1/requests';
const routes=[{method:'POST',path:endpoint,label:'提交服务申请'},{method:'GET',path:endpoint,label:'检索申请列表'},{method:'GET',path:endpoint+'/{id}',label:'查询处理进度'},{method:'POST',path:endpoint+'/{id}/withdraw',label:'撤回排队申请'}];
const snippet = `curl -X POST '${location.origin}/open/v1/requests' \\\n  -H 'Authorization: Bearer YOUR_API_KEY' \\\n  -H 'Idempotency-Key: your-unique-request-id' \\\n  -H 'Content-Type: application/json' \\\n  -d '{"authorized":true,"accounts":[{\n    "email":"account@example.com",\n    "password":"YOUR_PASSWORD",\n    "totp_secret":"YOUR_2FA_SECRET"\n  }]}'`;
async function load() {
  try {
    keys.value = (await api<{ items: ApiKey[] }>("/api/keys")).items;
  } catch (e) {
    error.value = (e as Error).message;
  }
}
async function create() {
  busy.value = true;
  error.value = "";
  try {
    token.value = (
      await api<{ token: string }>("/api/keys", {
        method: "POST",
        body: JSON.stringify({ name: keyName.value }),
      })
    ).token;
    await load();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
async function remove() {
  if (!revoke.value) return;
  busy.value = true;
  error.value = "";
  try {
    await api(`/api/keys/${revoke.value.id}`, { method: "DELETE" });
    revoke.value = undefined;
    await load();
    emit("toast", "访问密钥已停用");
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
async function clipboard(value: string) {
  try {
    await copy(value);
    emit("toast", "内容已复制");
  } catch (e) {
    emit("toast", (e as Error).message);
  }
}
function close() {
  if (busy.value) return;
  showCreate.value = false;
  token.value = "";
  keyName.value = "";
  error.value = "";
}
onMounted(load);
</script>
<template>
 <section class="developer-banner"><div class="developer-symbol"><Icon name="Terminal" :size="34"/></div><div><span class="eyebrow">HUIXU OPEN API</span><h2>一个接口入口，串联服务全过程。</h2><p>提交、查询、撤回，使用同一组凭证完成。</p></div><span class="badge gray">REST / V1</span></section>
 <div v-if="error&&!showCreate&&!revoke" class="error-banner" role="alert">{{error}}<button @click="load">再次读取</button></div>
 <section class="table-card key-panel"><div class="section-title"><div><h2>访问凭证</h2><p>为每个接入系统单独创建密钥，方便识别与停用。</p></div><button class="button primary" @click="showCreate=true;error=''">生成密钥<Icon name="Plus"/></button></div>
  <div v-if="keys.length" class="key-list"><div v-for="key in visible" :key="key.id"><span class="key-symbol"><Icon name="KeyRound"/></span><div><strong>{{key.name}}</strong><small>{{key.prefix}}•••••• · {{date(key.created_at)}} 创建<template v-if="key.last_used_at"> · 最近调用 {{date(key.last_used_at)}}</template></small></div><span class="badge" :class="key.revoked_at?'gray':'green'">{{key.revoked_at?'已停用':'可调用'}}</span><button v-if="!key.revoked_at" class="text-button danger" @click="revoke=key;error=''">停用</button></div></div>
  <div v-else class="empty"><Icon name="KeyRound" :size="28"/><h3>还未配置访问凭证</h3><p>生成第一把密钥，开始对接你的服务。</p></div>
  <Pagination v-model:page="page" v-model:size="size" :total="keys.length" label="访问凭证分页"/>
 </section>
 <div class="developer-workbench"><section class="endpoint-directory"><div class="section-title"><div><span class="eyebrow">ENDPOINTS</span><h2>接口目录</h2></div><span>04</span></div><div v-for="r in routes" :key="r.method+r.path" class="endpoint-row"><div><span class="method-tag" :class="r.method.toLowerCase()">{{r.method}}</span><strong>{{r.label}}</strong><button class="icon-button" :aria-label="'复制'+r.label+'地址'" @click="clipboard(r.path)"><Icon name="Copy" :size="14"/></button></div><code>{{r.path}}</code></div><p class="endpoint-note">将 {id} 换成提交返回的申请编号。查询建议间隔 15 秒，处理完成后停止轮询。</p></section>
  <section class="code-card"><div><span><Icon name="Terminal" :size="16"/>提交示例 / cURL</span><button class="icon-button" aria-label="复制请求示例" @click="clipboard(snippet)"><Icon name="Copy" :size="16"/></button></div><pre>{{snippet}}</pre><footer>使用服务端环境调用，密钥请勿写入公开网页。</footer></section>
 </div>
 <div class="notice"><Icon name="CircleAlert" :size="20"/><div>先确认受理，再读取结果。<small>HTTP 202 说明请求已收件；请根据进度接口返回的最终状态判断服务结果。重复提交时沿用原幂等键。</small></div></div>
 <Modal v-if="showCreate" title="生成访问密钥" description="填写用途名称，便于后续管理。" :busy="busy" @close="close"><form v-if="!token" @submit.prevent="create"><div class="modal-body"><label>用途名称<input v-model="keyName" placeholder="例如：订单系统" maxlength="40" required/></label><p v-if="error" class="form-error" role="alert">{{error}}</p></div><footer class="modal-actions"><button type="button" class="button secondary" :disabled="busy" @click="close">暂不创建</button><button class="button primary" :disabled="busy||!keyName.trim()">{{busy?'生成中…':'确认生成'}}</button></footer></form><template v-else><div class="modal-body"><p>请现在保存密钥。离开此窗口后，完整内容将不再显示。</p><div class="secret-box"><code>{{token}}</code><button class="icon-button" aria-label="复制访问密钥" @click="clipboard(token)"><Icon name="Copy"/></button></div></div><footer class="modal-actions"><button class="button primary" @click="close">已安全保存</button></footer></template></Modal>
 <Modal v-if="revoke" title="停用访问密钥" :busy="busy" @close="!busy&&(revoke=undefined)"><div class="modal-body"><p>停用「{{revoke.name}}」后，使用它的接口请求会立即失去访问权限。</p><p v-if="error" class="form-error">{{error}}</p></div><footer class="modal-actions"><button class="button secondary" :disabled="busy" @click="revoke=undefined">继续使用</button><button class="button primary" :disabled="busy" @click="remove">确认停用</button></footer></Modal>
</template>

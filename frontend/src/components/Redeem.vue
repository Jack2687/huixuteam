<script setup lang="ts">
import { ref } from "vue";
import { api, money } from "../lib/api";
import Icon from "./Icon.vue";
const emit = defineEmits<{ redeemed: [message: string] }>();
const code = ref(""),
  busy = ref(false),
  error = ref(""),
  success = ref("");
async function redeem() {
  if (!code.value.trim() || busy.value) return;
  busy.value = true;
  error.value = "";
  success.value = "";
  try {
    const data = await api<{ amount: number }>("/api/redeem", {
      method: "POST",
      body: JSON.stringify({ code: code.value.trim() }),
    });
    code.value = "";
    success.value = `兑换成功，${money(data.amount)} 分已存入可支配积分。`;
    emit("redeemed", success.value);
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <section class="redeem-card">
    <div>
      <span class="key-symbol"><Icon name="KeyRound" :size="21" /></span>
      <div>
        <h2>兑换入账</h2>
        <p>使用有效兑换码，为账户增加可用积分。</p>
      </div>
    </div>
    <form @submit.prevent="redeem">
      <label class="sr-only" for="redeem-code">兑换码</label
      ><input
        id="redeem-code"
        v-model="code"
        placeholder="粘贴完整兑换码"
        autocomplete="off"
        maxlength="100"
        :disabled="busy"
        required
      /><button class="button primary" :disabled="busy || !code.trim()">
        {{ busy ? "正在核验…" : "兑换到本账户" }}<Icon name="ArrowUpRight" />
      </button>
    </form>
    <p v-if="error" class="form-error" role="alert">{{ error }}</p>
    <p v-if="success" class="redeem-success" role="status">
      <Icon name="Check" :size="15" />{{ success }}
    </p>
    <small>每个账户对同一码限用一次，兑换成功后可查阅收支记录。</small>
  </section>
</template>

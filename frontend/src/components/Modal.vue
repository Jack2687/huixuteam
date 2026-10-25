<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import Icon from "./Icon.vue";
defineProps<{
  title: string;
  description?: string;
  wide?: boolean;
  busy?: boolean;
}>();
const emit = defineEmits<{ close: [] }>();
const el = ref<HTMLElement>();
const previous = document.activeElement as HTMLElement | null;
const oldOverflow = document.body.style.overflow;
function key(event: KeyboardEvent) {
  if (event.key === "Escape") emit("close");
  if (event.key === "Tab") {
    const elements = Array.from(
      el.value?.querySelectorAll<HTMLElement>(
        'button:not(:disabled),input:not(:disabled),textarea,select,a[href],[tabindex="0"]',
      ) || [],
    ).filter((e) => e.getClientRects().length);
    const first = elements[0],
      last = elements.at(-1);
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last?.focus();
    }
    if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first?.focus();
    }
  }
}
onMounted(() => {
  document.body.style.overflow = "hidden";
  el.value?.focus();
  document.addEventListener("keydown", key);
});
onUnmounted(() => {
  document.body.style.overflow = oldOverflow;
  document.removeEventListener("keydown", key);
  previous?.focus();
});
</script>
<template>
  <Teleport to="body"
    ><div class="modal-overlay" @mousedown.self="!busy && emit('close')">
      <section
        ref="el"
        class="modal"
        :class="{ wide }"
        role="dialog"
        aria-modal="true"
        aria-labelledby="modal-title"
        tabindex="-1"
      >
        <header>
          <div>
            <h2 id="modal-title">{{ title }}</h2>
            <p v-if="description">{{ description }}</p>
          </div>
          <button
            class="icon-button"
            aria-label="关闭"
            :disabled="busy"
            @click="emit('close')"
          >
            <Icon name="X" :size="20" />
          </button>
        </header>
        <slot />
      </section></div
  ></Teleport>
</template>

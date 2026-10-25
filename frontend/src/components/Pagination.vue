<script setup lang="ts">
import { computed } from "vue";
import Icon from "./Icon.vue";
const props = withDefaults(
  defineProps<{
    page: number;
    size: number;
    total: number;
    label?: string;
    sizes?: number[];
  }>(),
  { label: "列表分页", sizes: () => [20, 50, 100] },
);
const emit = defineEmits<{
  "update:page": [value: number];
  "update:size": [value: number];
}>();
const pages = computed(() => Math.max(1, Math.ceil(props.total / props.size)));
const visible = computed(() =>
  Array.from(
    { length: Math.min(5, pages.value) },
    (_, i) => Math.max(1, Math.min(props.page - 2, pages.value - 4)) + i,
  ),
);
</script>
<template>
  <nav class="list-pagination" :aria-label="label">
    <span class="list-pagination-summary" aria-live="polite"
      >共 {{ total }} 条 · 第 {{ page }} 页</span
    >
    <div class="list-pagination-controls">
      <select
        :value="size"
        aria-label="每页条数"
        @change="
          emit(
            'update:size',
            Number(($event.target as HTMLSelectElement).value),
          );
          emit('update:page', 1);
        "
      >
        <option v-for="n in sizes" :key="n" :value="n">
          {{ n }} 条 / 页
        </option></select
      ><button
        class="list-page-button"
        :disabled="page <= 1"
        aria-label="上一页"
        @click="emit('update:page', page - 1)"
      >
        <Icon name="ChevronLeft" :size="14" />上一页
      </button>
      <div class="list-page-numbers">
        <button
          v-for="n in visible"
          :key="n"
          class="list-page-button"
          :aria-current="n === page ? 'page' : undefined"
          :aria-label="`第 ${n} 页`"
          @click="emit('update:page', n)"
        >
          {{ n }}
        </button>
      </div>
      <button
        class="list-page-button"
        :disabled="page >= pages"
        aria-label="下一页"
        @click="emit('update:page', page + 1)"
      >
        下一页<Icon name="ChevronRight" :size="14" />
      </button>
    </div>
  </nav>
</template>

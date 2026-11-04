<script setup lang="ts">
import { CheckCircle2, Info, TriangleAlert, XCircle } from "lucide-vue-next";
import { computed } from "vue";

const props = defineProps<{
  tone: "success" | "warning" | "error" | "info";
  title: string;
}>();

const icon = computed(() => ({ success: CheckCircle2, warning: TriangleAlert, error: XCircle, info: Info })[props.tone]);
</script>

<template>
  <div class="notice card" :class="`notice--${tone}`" :role="tone === 'error' ? 'alert' : 'status'">
    <component :is="icon" class="icon" :size="18" aria-hidden="true" />
    <div>
      <p class="title">{{ title }}</p>
      <p v-if="$slots.default" class="muted small"><slot /></p>
    </div>
  </div>
</template>

<style scoped>
.notice {
  display: flex;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
}

.icon {
  flex: none;
  margin-top: 2px;
}

.title {
  font-weight: 500;
}

.notice--success .icon {
  color: var(--color-success);
}

.notice--warning .icon {
  color: var(--color-warning);
}

.notice--error .icon {
  color: var(--color-error);
}

.notice--warning {
  border-color: color-mix(in srgb, var(--color-warning) 30%, transparent);
  background: color-mix(in srgb, var(--color-warning) 8%, var(--color-surface));
}

.notice--error {
  border-color: color-mix(in srgb, var(--color-error) 30%, transparent);
  background: color-mix(in srgb, var(--color-error) 6%, var(--color-surface));
}

.notice--info .icon {
  color: var(--color-text-muted);
}
</style>

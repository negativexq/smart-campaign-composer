<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from "vue";

// Asks before an action that takes something out of sight. The caller does the work
// and passes busy and problem back, so a refusal is read here instead of lost.
const props = defineProps<{ title: string; confirmLabel: string; busy?: boolean; problem?: string; danger?: boolean; testId?: string }>();
const emit = defineEmits<{ close: []; confirm: [] }>();
const dialog = ref<HTMLElement | null>(null);

function onKey(event: KeyboardEvent) {
  if (event.key === "Escape" && !props.busy) emit("close");
}

onMounted(async () => {
  document.addEventListener("keydown", onKey);
  await nextTick();
  // Cancel takes the focus, so Enter alone does not delete anything.
  dialog.value?.querySelector<HTMLElement>("button")?.focus();
});
onBeforeUnmount(() => document.removeEventListener("keydown", onKey));
</script>

<template>
  <div class="backdrop" @click.self="!busy && emit('close')">
    <div ref="dialog" class="dialog" role="alertdialog" aria-modal="true" aria-labelledby="confirm-title" :data-testid="testId ?? 'confirm-dialog'">
      <h2 id="confirm-title" class="title">{{ title }}</h2>
      <div class="body small"><slot /></div>
      <p v-if="problem" class="error small" role="alert">{{ problem }}</p>
      <div class="actions">
        <button class="button button--secondary" type="button" :disabled="busy" @click="emit('close')">Cancel</button>
        <button class="button" :class="danger ? 'button--danger' : 'button--primary'" type="button" data-testid="confirm-action" :disabled="busy" @click="emit('confirm')">
          {{ confirmLabel }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.backdrop {
  position: fixed;
  inset: 0;
  z-index: 50;
  display: grid;
  place-items: center;
  padding: var(--space-4);
  background: rgb(0 0 0 / 40%);
}

.dialog {
  display: grid;
  gap: var(--space-3);
  width: min(460px, 100%);
  padding: var(--space-6);
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
}

.title {
  margin: 0;
  font-size: 1.125rem;
}

.body {
  display: grid;
  gap: var(--space-2);
  color: var(--color-text-muted);
}

.body :deep(p) {
  margin: 0;
}

.error {
  margin: 0;
  color: var(--color-error);
}

.actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}

.button--danger {
  color: #fff;
  background: var(--color-error);
}

.button--danger:hover:not(:disabled) {
  filter: brightness(0.92);
}
</style>

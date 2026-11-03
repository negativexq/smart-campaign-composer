<script setup lang="ts">
import { PencilLine, Sparkles } from "lucide-vue-next";

export type ContentOrigin = "ai" | "user";

defineProps<{ origin: ContentOrigin }>();
</script>

<template>
  <span class="origin small" :class="`origin--${origin}`">
    <Sparkles v-if="origin === 'ai'" :size="14" aria-hidden="true" />
    <PencilLine v-else :size="14" aria-hidden="true" />
    {{ origin === "ai" ? "AI suggested" : "Edited by you" }}
  </span>
</template>

<style scoped>
.origin {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  white-space: nowrap;
}

.origin--ai {
  color: var(--color-ai);
}

.origin--user {
  color: var(--color-text-muted);
}
</style>

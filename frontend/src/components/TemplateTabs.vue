<script setup lang="ts">
defineProps<{ active: string }>();

const TABS = [
  { href: "/templates", label: "Templates" },
  { href: "/templates/images", label: "Images" },
  { href: "/templates/brand", label: "Brand kit" },
];
</script>

<template>
  <nav class="tabs" aria-label="Templates sections" data-testid="template-tabs">
    <a v-for="tab in TABS" :key="tab.href" class="tab" :class="{ 'tab--on': active === tab.href }" :href="tab.href" :aria-current="active === tab.href ? 'page' : undefined">{{ tab.label }}</a>
  </nav>
</template>

<style scoped>
.tabs {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  margin-bottom: var(--space-6);
}

.tab {
  display: inline-flex;
  align-items: center;
  min-height: 32px;
  padding: 0 var(--space-3);
  color: var(--color-text-muted);
  border-radius: var(--radius-sm);
  text-decoration: none;
}

.tab:hover {
  background: var(--color-surface-subtle);
}

.tab--on {
  color: var(--color-primary);
  background: var(--color-primary-soft);
  font-weight: 500;
}
</style>

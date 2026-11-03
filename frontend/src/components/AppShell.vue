<script setup lang="ts">
import { LayoutGrid, PenLine } from "lucide-vue-next";

import type { SystemStatus } from "../api/system";

defineProps<{
  activePath: string;
  title: string;
  campaignStatus?: string;
  systemStatus: SystemStatus | "checking";
}>();

const links = [
  { href: "/", label: "New campaign", icon: PenLine },
  { href: "/design", label: "Design system", icon: LayoutGrid },
];

const statusLabels = { checking: "Checking", operational: "Operational", unavailable: "Unavailable" };
</script>

<template>
  <div class="shell">
    <aside class="sidebar">
      <a class="brand" href="/">Smart Campaign Composer</a>
      <nav class="nav" aria-label="Main">
        <a
          v-for="link in links"
          :key="link.href"
          class="nav-item"
          :class="{ 'nav-item--active': link.href === activePath }"
          :href="link.href"
          :aria-current="link.href === activePath ? 'page' : undefined"
        >
          <component :is="link.icon" :size="16" aria-hidden="true" />
          {{ link.label }}
        </a>
      </nav>
      <div class="system small">
        <span class="dot" :class="`dot--${systemStatus}`" aria-hidden="true"></span>
        <span data-testid="system-status">{{ statusLabels[systemStatus] }}</span>
      </div>
    </aside>

    <div class="workspace">
      <header class="topbar">
        <div class="topbar-title">
          <span class="title-text">{{ title }}</span>
          <span v-if="campaignStatus" class="status small" data-testid="campaign-status">{{ campaignStatus }}</span>
        </div>
        <slot name="topbar" />
      </header>

      <div class="content" :class="{ 'content--with-panel': $slots.panel }">
        <main class="main"><slot /></main>
        <aside v-if="$slots.panel" class="panel"><slot name="panel" /></aside>
      </div>
    </div>
  </div>
</template>

<style scoped>
.shell {
  display: grid;
  grid-template-columns: var(--sidebar-width) minmax(0, 1fr);
  max-width: var(--canvas-max-width);
  min-height: 100vh;
  margin: 0 auto;
}

.sidebar {
  position: sticky;
  top: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  height: 100vh;
  padding: var(--space-4) var(--space-3);
  background: var(--color-surface);
  border-right: 1px solid var(--color-border);
}

.brand {
  padding: var(--space-1) var(--space-2);
  color: var(--color-text);
  font-weight: 600;
  line-height: 1.3;
  text-decoration: none;
}

.nav {
  display: grid;
  gap: var(--space-1);
}

.nav-item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 36px;
  padding: 0 var(--space-2);
  color: var(--color-text-muted);
  border-radius: var(--radius-sm);
  text-decoration: none;
  transition: background-color var(--transition-fast);
}

.nav-item:hover {
  background: var(--color-surface-subtle);
}

.nav-item--active {
  color: var(--color-primary);
  background: var(--color-primary-soft);
  font-weight: 500;
}

.system {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-top: auto;
  padding: var(--space-1) var(--space-2);
  color: var(--color-text-muted);
}

.dot {
  width: 8px;
  height: 8px;
  background: var(--color-border-input);
  border-radius: 50%;
}

.dot--operational {
  background: var(--color-success);
}

.dot--unavailable {
  background: var(--color-error);
}

.topbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2) var(--space-6);
  min-height: var(--topbar-height);
  padding: var(--space-3) var(--space-8);
  background: var(--color-surface);
  border-bottom: 1px solid var(--color-border);
}

.topbar-title {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.title-text {
  font-weight: 600;
}

.status {
  padding: 2px var(--space-2);
  color: var(--color-text-muted);
  background: var(--color-surface-subtle);
  border-radius: var(--radius-sm);
  font-weight: 500;
}

.content {
  display: grid;
  gap: var(--space-8);
  padding: var(--space-8);
}

.content--with-panel {
  grid-template-columns: minmax(0, 1fr) var(--panel-width);
}

.main {
  min-width: 0;
}

@media (max-width: 1200px) {
  .content--with-panel {
    grid-template-columns: minmax(0, 1fr);
  }
}

@media (max-width: 760px) {
  .shell {
    grid-template-columns: minmax(0, 1fr);
  }

  .sidebar {
    position: static;
    flex-flow: row wrap;
    align-items: center;
    height: auto;
    padding: var(--space-2) var(--space-4);
    border-right: 0;
    border-bottom: 1px solid var(--color-border);
  }

  .brand {
    padding: 0;
  }

  .nav {
    display: none;
  }

  .system {
    margin: 0 0 0 auto;
    padding: 0;
  }

  .topbar {
    padding: var(--space-3) var(--space-4);
  }

  .content {
    padding: var(--space-6) var(--space-4);
  }
}
</style>

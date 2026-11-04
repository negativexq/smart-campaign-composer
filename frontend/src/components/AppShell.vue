<script setup lang="ts">
import { Gauge, Inbox, LayoutGrid, ListChecks, Plus } from "lucide-vue-next";

import { endSession } from "../api/session";
import type { SystemStatus } from "../api/system";
import { currentUser } from "../lib/session";
import NotificationBell from "./NotificationBell.vue";

defineProps<{
  activePath: string;
  title: string;
  campaignStatus?: string;
  systemStatus: SystemStatus | "checking";
}>();

const groups = [
  {
    title: "Workspace",
    links: [
      { href: "/", label: "Overview", icon: Gauge },
      { href: "/campaigns", label: "Campaigns", icon: ListChecks },
    ],
  },
  {
    title: "Reference",
    links: [
      { href: "/design", label: "Design system", icon: LayoutGrid },
      // The mock provider serves it, so it is outside this app.
      { href: `${location.protocol}//${location.hostname}:8083/`, label: "Test inbox", icon: Inbox, external: true },
    ],
  },
];

async function signOut() {
  try {
    await endSession();
  } finally {
    currentUser.value = null;
  }
}

const statusLabels = { checking: "Checking", operational: "Operational", degraded: "Sending affected", unavailable: "Unavailable" };
</script>

<template>
  <div class="shell">
    <aside class="sidebar">
      <a class="brand" href="/">Smart Campaign Composer</a>
      <a class="button button--primary new-campaign" href="/campaigns/new" data-testid="new-campaign">
        <Plus :size="16" aria-hidden="true" />
        New campaign
      </a>
      <nav class="nav" aria-label="Main">
        <div
          v-for="group in groups"
          :key="group.title"
          class="nav-group"
          role="group"
          :aria-labelledby="`nav-${group.title}`"
          data-testid="nav-group"
        >
          <div :id="`nav-${group.title}`" class="nav-title" data-testid="nav-group-title">{{ group.title }}</div>
          <a
            v-for="link in group.links"
            :key="link.href"
            class="nav-item"
            :class="{ 'nav-item--active': link.href === activePath }"
            :href="link.href"
            :target="'external' in link ? '_blank' : undefined"
            :rel="'external' in link ? 'noopener' : undefined"
            :aria-current="link.href === activePath ? 'page' : undefined"
          >
            <component :is="link.icon" :size="16" aria-hidden="true" />
            {{ link.label }}
          </a>
        </div>
      </nav>
      <div v-if="currentUser" class="account small">
        <span class="account-name" data-testid="current-user" :title="currentUser">{{ currentUser }}</span>
        <button class="sign-out" type="button" data-testid="sign-out" @click="signOut">Sign out</button>
      </div>
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
        <div class="topbar-end">
          <slot name="topbar" />
          <NotificationBell />
        </div>
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

.new-campaign {
  justify-content: center;
  text-decoration: none;
}

.nav {
  display: flex;
  flex: 1;
  flex-direction: column;
  justify-content: space-between;
  min-height: 0;
  margin-top: var(--space-2);
}

.nav-group {
  display: grid;
  gap: var(--space-1);
}

.nav-title {
  padding: 0 var(--space-2) var(--space-1);
  color: var(--color-text-muted);
  font-size: 12px;
  font-weight: 500;
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
  padding: var(--space-3) var(--space-2) var(--space-1);
  color: var(--color-text-muted);
  border-top: 1px solid var(--color-border);
}

.dot {
  width: 8px;
  height: 8px;
  background: var(--color-border-input);
  border-radius: 50%;
}

.account {
  display: grid;
  gap: var(--space-1);
  min-width: 0;
  padding-top: var(--space-3);
  border-top: 1px solid var(--color-border);
}

.account-name {
  overflow: hidden;
  font-weight: 500;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sign-out {
  justify-self: start;
  padding: 0;
  color: var(--color-primary);
  background: transparent;
  border: 0;
  font: inherit;
  cursor: pointer;
}

.dot--operational {
  background: var(--color-success);
}

.dot--degraded {
  background: var(--color-warning);
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

.topbar-end {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
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
    grid-template-rows: auto 1fr;
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

  .new-campaign {
    order: 3;
    width: 100%;
  }

  .nav {
    order: 4;
    flex-basis: 100%;
    margin-top: 0;
  }

  .nav-group {
    display: flex;
    flex-wrap: wrap;
  }

  .nav-title {
    display: none;
  }

  .nav-group:last-child {
    display: none;
  }

  .system {
    margin: 0 0 0 auto;
    padding: 0;
    border-top: 0;
  }

  .topbar {
    padding: var(--space-3) var(--space-4);
  }

  .content {
    padding: var(--space-6) var(--space-4);
  }
}
</style>

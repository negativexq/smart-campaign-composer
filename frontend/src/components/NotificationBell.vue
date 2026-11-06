<script setup lang="ts">
import { Bell, CircleAlert, CircleCheck, Clock } from "lucide-vue-next";
import { computed, onBeforeUnmount, onMounted, ref } from "vue";

import { getNotifications, type NotificationEntry } from "../api/campaigns";
import { describeActivity, notificationTone } from "../lib/activity";
import { formatDateTime } from "../lib/format";
import { everyWhileVisible } from "../lib/visibility";

const CHECK_EVERY = 15000;
const SEEN_KEY = "notifications-seen";
const CLEARED_KEY = "notifications-cleared";
const ICONS = { error: CircleAlert, done: CircleCheck, started: Clock };

// What was seen stays in this browser, since nobody here has a real account.
function readMark(key: string) {
  try {
    return Number(localStorage.getItem(key) ?? 0);
  } catch {
    return 0;
  }
}

function saveMark(key: string, value: number) {
  try {
    localStorage.setItem(key, String(value));
  } catch {
    // Then it comes back on the next visit.
  }
}

const entries = ref<NotificationEntry[]>([]);
const seen = ref(readMark(SEEN_KEY));
const cleared = ref(readMark(CLEARED_KEY));
const open = ref(false);
const root = ref<HTMLElement | null>(null);
// Clearing removes only what ended well. A problem stays until it is put right.
const shown = computed(() => entries.value.filter((entry) => notificationTone(entry) !== "done" || entry.id > cleared.value));
const canClear = computed(() => shown.value.some((entry) => notificationTone(entry) === "done"));
const unread = computed(() => shown.value.filter((entry) => entry.id > seen.value).length);
let stopChecking = () => {};

async function load() {
  try {
    entries.value = await getNotifications();
  } catch {
    // The list stays as it was, and the next check tries again.
  }
}

function markSeen() {
  seen.value = Math.max(seen.value, entries.value[0]?.id ?? 0);
  saveMark(SEEN_KEY, seen.value);
}

function clear() {
  cleared.value = Math.max(cleared.value, entries.value[0]?.id ?? 0);
  saveMark(CLEARED_KEY, cleared.value);
}

function toggle() {
  open.value = !open.value;
  if (open.value) markSeen();
}

function closeOutside(event: MouseEvent) {
  // The path, not the target: a button that removes itself is no longer inside by now.
  if (open.value && root.value && !event.composedPath().includes(root.value)) open.value = false;
}

function closeOnEscape(event: KeyboardEvent) {
  if (event.key === "Escape") open.value = false;
}

onMounted(() => {
  void load();
  stopChecking = everyWhileVisible(load, CHECK_EVERY);
  document.addEventListener("click", closeOutside);
  document.addEventListener("keydown", closeOnEscape);
});
onBeforeUnmount(() => {
  stopChecking();
  document.removeEventListener("click", closeOutside);
  document.removeEventListener("keydown", closeOnEscape);
});
</script>

<template>
  <div ref="root" class="notifications">
    <button
      class="bell"
      type="button"
      data-testid="notifications-toggle"
      :aria-expanded="open"
      :aria-label="unread ? `Notifications, ${unread} new` : 'Notifications'"
      @click="toggle"
    >
      <Bell :size="18" aria-hidden="true" />
      <span v-if="unread" class="count" data-testid="notifications-count">{{ unread > 9 ? "9+" : unread }}</span>
    </button>

    <section v-if="open" class="panel card" aria-label="Notifications" data-testid="notifications-panel">
      <div class="head">
        <h2 class="heading">Notifications</h2>
        <button v-if="canClear" class="clear small" type="button" data-testid="notifications-clear" @click="clear">Clear</button>
      </div>
      <p v-if="!shown.length" class="muted small empty" data-testid="notifications-empty">Finished sends, scheduled starts and problems appear here.</p>
      <TransitionGroup name="note" tag="ul" class="list">
        <li v-for="entry in shown" :key="entry.id" class="note">
          <a class="item" :href="`/campaigns/${entry.campaign_id}`" data-testid="notification" :data-tone="notificationTone(entry)">
            <component :is="ICONS[notificationTone(entry)]" :size="16" class="icon" :class="`icon--${notificationTone(entry)}`" aria-hidden="true" />
            <span class="text">
              <strong>{{ describeActivity(entry).title }}</strong>
              <span class="small">{{ entry.campaign_name }}</span>
              <span v-if="describeActivity(entry).detail" class="small muted">{{ describeActivity(entry).detail }}</span>
              <span class="small muted">{{ formatDateTime(entry.occurred_at) }}</span>
            </span>
          </a>
        </li>
      </TransitionGroup>
    </section>
  </div>
</template>

<style scoped>
.notifications {
  position: relative;
  flex: none;
}

.bell {
  position: relative;
  display: grid;
  place-items: center;
  width: 36px;
  height: 36px;
  color: var(--color-text);
  background: none;
  border: 0;
  border-radius: var(--radius-md);
  cursor: pointer;
}

.bell:hover {
  background: var(--color-surface-subtle);
}

.count {
  position: absolute;
  top: 1px;
  right: 1px;
  min-width: 16px;
  padding: 0 4px;
  color: var(--color-on-primary);
  font-size: 11px;
  font-weight: 600;
  line-height: 16px;
  text-align: center;
  background: var(--color-primary);
  border-radius: 8px;
}

.panel {
  position: absolute;
  top: calc(100% + var(--space-1));
  right: 0;
  z-index: 20;
  width: min(360px, calc(100vw - 32px));
  max-height: 70vh;
  overflow-y: auto;
  box-shadow: 0 8px 24px rgb(0 0 0 / 12%);
}

.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--color-border);
}

.heading {
  margin: 0;
  font-size: inherit;
  font-weight: 600;
}

.clear {
  padding: 0;
  color: var(--color-primary);
  background: none;
  border: 0;
  cursor: pointer;
}

.empty {
  padding: var(--space-4);
}

.list {
  margin: 0;
  padding: 0;
  list-style: none;
}

.note {
  overflow: hidden;
}

.note-leave-active {
  transition:
    opacity 0.2s ease,
    max-height 0.3s ease 0.1s;
}

.note-leave-from {
  max-height: 200px;
}

.note-leave-to {
  max-height: 0;
  opacity: 0;
}

@media (prefers-reduced-motion: reduce) {
  .note-leave-active {
    transition: none;
  }
}

.item {
  display: flex;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
  color: var(--color-text);
  text-decoration: none;
  border-bottom: 1px solid var(--color-border);
}

.item:hover {
  background: var(--color-surface-subtle);
}

.icon {
  flex: none;
  margin-top: 3px;
}

.icon--error {
  color: var(--color-error);
}

.icon--done {
  color: var(--color-success);
}

.icon--started {
  color: var(--color-primary);
}

.text {
  display: grid;
  gap: 2px;
  min-width: 0;
  overflow-wrap: anywhere;
}

.text strong {
  font-weight: 500;
}
</style>

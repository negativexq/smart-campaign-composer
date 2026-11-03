<script setup lang="ts">
import { onMounted, ref } from "vue";

import { fetchSystemStatus, type SystemStatus } from "./api/system";

const brief = ref("");
const status = ref<SystemStatus | "checking">("checking");

const statusLabels = {
  checking: "Checking",
  operational: "Operational",
  unavailable: "Unavailable",
};

onMounted(async () => {
  status.value = await fetchSystemStatus();
});
</script>

<template>
  <div class="shell">
    <aside class="sidebar">
      <div class="workspace">Campaign Composer</div>
      <nav>
        <a class="nav-item nav-item--active" href="/" aria-current="page">New campaign</a>
      </nav>
      <div class="sidebar-footer">
        <span class="dot" :class="`dot--${status}`" aria-hidden="true"></span>
        <span data-testid="system-status">{{ statusLabels[status] }}</span>
      </div>
    </aside>

    <main class="page">
      <h1>New campaign</h1>
      <p class="lede">Say what you want to send and to whom. You review everything before it goes out.</p>

      <label class="field-label" for="brief">Brief</label>
      <textarea
        id="brief"
        v-model="brief"
        class="brief"
        rows="5"
        placeholder="Describe your campaign, for example: remind Istanbul users who added to cart in the last 30 days but did not buy about a 30% discount. Keep it friendly and short."
      ></textarea>

      <div class="actions">
        <button type="button" class="button" disabled>Find audience</button>
        <span class="hint" data-testid="action-hint">Audience generation is not available yet.</span>
      </div>
    </main>
  </div>
</template>

<style scoped>
.shell {
  display: grid;
  grid-template-columns: 232px minmax(0, 1fr);
  min-height: 100vh;
}

.sidebar {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 14px 10px;
  background: var(--surface-muted);
  border-right: 1px solid var(--border);
}

.workspace {
  padding: 4px 8px;
  font-weight: 600;
}

.nav-item {
  display: block;
  padding: 5px 8px;
  border-radius: 6px;
  color: var(--text);
  text-decoration: none;
}

.nav-item--active {
  background: var(--surface-hover);
  font-weight: 500;
}

.sidebar-footer {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: auto;
  padding: 4px 8px;
  color: var(--text-muted);
  font-size: 12px;
}

.dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--text-faint);
}

.dot--operational {
  background: var(--success);
}

.dot--unavailable {
  background: var(--danger);
}

.page {
  width: 100%;
  max-width: 720px;
  margin: 0 auto;
  padding: 72px 32px;
}

h1 {
  margin: 0 0 6px;
  font-size: 28px;
  font-weight: 600;
  letter-spacing: -0.02em;
}

.lede {
  margin: 0 0 36px;
  color: var(--text-muted);
}

.field-label {
  display: block;
  margin-bottom: 6px;
  font-size: 12px;
  font-weight: 500;
  color: var(--text-muted);
}

.brief {
  display: block;
  width: 100%;
  padding: 10px 12px;
  resize: vertical;
  color: var(--text);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  font: inherit;
  line-height: 1.55;
}

.brief::placeholder {
  color: var(--text-faint);
}

.brief:focus {
  outline: none;
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--accent-ring);
}

.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  margin-top: 14px;
}

.button {
  padding: 6px 12px;
  color: var(--accent-contrast);
  background: var(--accent);
  border: 0;
  border-radius: 6px;
  font: inherit;
  font-weight: 500;
  cursor: pointer;
}

.button:disabled {
  color: var(--text-faint);
  background: var(--surface-hover);
  cursor: not-allowed;
}

.hint {
  color: var(--text-muted);
  font-size: 12px;
}

@media (max-width: 720px) {
  .shell {
    grid-template-columns: minmax(0, 1fr);
    grid-template-rows: auto 1fr;
  }

  .sidebar {
    flex-direction: row;
    align-items: center;
    border-right: 0;
    border-bottom: 1px solid var(--border);
  }

  .sidebar nav {
    display: none;
  }

  .sidebar-footer {
    margin: 0 0 0 auto;
  }

  .page {
    padding: 40px 16px;
  }
}
</style>

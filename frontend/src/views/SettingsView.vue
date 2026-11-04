<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";

import { errorMessage, getSettings, type AppSettings } from "../api/campaigns";
import ValidationNotice from "../components/ValidationNotice.vue";
import { AUTO_REFRESH, THEMES, defaultChannels, storeAutoRefresh, storeDefaultChannels, storeTheme, storedAutoRefresh, storedTheme } from "../lib/preferences";

const settings = ref<AppSettings | null>(null);
const error = ref("");
const theme = ref(storedTheme());
const autoRefresh = ref(storedAutoRefresh());
const channels = ref(defaultChannels());
// A campaign needs a channel, so the last one stays ticked.
const isLastChannel = (channel: string) => channels.value.length === 1 && channels.value[0] === channel;
const saved = ref(false);
const testInbox = `${location.protocol}//${location.hostname}:8083/`;

const isMock = computed(() => settings.value?.llm_provider === "mock");
const providerName = computed(() => ({ mock: "Built-in test model", anthropic: "Anthropic", openai: "OpenAI" })[settings.value?.llm_provider ?? ""] ?? settings.value?.llm_provider);

watch(theme, (value) => {
  storeTheme(value);
  saved.value = true;
});
watch(autoRefresh, (value) => {
  storeAutoRefresh(value);
  saved.value = true;
});
watch(channels, (value) => {
  storeDefaultChannels(value);
  saved.value = true;
});

onMounted(async () => {
  try {
    settings.value = await getSettings();
  } catch (failure) {
    error.value = errorMessage(failure);
  }
});
</script>

<template>
  <div class="settings">
    <header class="intro">
      <h1>See how this workspace is set up.</h1>
      <p class="muted">Your preferences apply right away in this browser. The AI and sending settings are fixed when the app starts, so they are changed in its environment file.</p>
    </header>

    <ValidationNotice v-if="error" tone="error" title="We could not load the settings">{{ error }}</ValidationNotice>
    <p v-else-if="!settings" class="muted" role="status">Loading settings...</p>
    <template v-else>
      <section class="section">
        <div class="section-head">
          <h2>Preferences</h2>
          <span v-if="saved" class="small saved" role="status" data-testid="settings-saved">Saved in this browser</span>
        </div>
        <div class="card rows">
          <div>
            <label class="muted" for="settings-theme">Appearance</label>
            <select id="settings-theme" v-model="theme" class="field select" data-testid="settings-theme">
              <option v-for="option in THEMES" :key="option.value" :value="option.value">{{ option.label }}</option>
            </select>
          </div>
          <div>
            <span id="settings-channels" class="muted">Channels in a new campaign</span>
            <div class="checks" role="group" aria-labelledby="settings-channels">
              <label class="check">
                <input v-model="channels" type="checkbox" value="sms" :disabled="isLastChannel('sms')" data-testid="settings-channel-sms" />
                SMS
              </label>
              <label class="check">
                <input v-model="channels" type="checkbox" value="email" :disabled="isLastChannel('email')" data-testid="settings-channel-email" />
                Email
              </label>
            </div>
          </div>
          <div>
            <label class="muted" for="settings-auto-refresh">Delivery monitor auto refresh</label>
            <select id="settings-auto-refresh" v-model="autoRefresh" class="field select" data-testid="settings-auto-refresh">
              <option v-for="option in AUTO_REFRESH" :key="option.value" :value="option.value">{{ option.label }}</option>
            </select>
          </div>
        </div>
      </section>

      <section class="section">
        <h2>AI provider</h2>
        <dl class="card rows">
          <div><dt class="muted">Provider</dt><dd data-testid="settings-provider">{{ providerName }}</dd></div>
          <div v-if="!isMock"><dt class="muted">Model</dt><dd class="mono" data-testid="settings-model">{{ settings.llm_model }}</dd></div>
          <div><dt class="muted">To change it</dt><dd>Set <code class="mono">LLM_PROVIDER</code> and its API key, then restart the app.</dd></div>
        </dl>
      </section>

      <section class="section">
        <h2>Sending</h2>
        <dl class="card rows">
          <div><dt class="muted">Most messages per second</dt><dd data-testid="settings-rate">{{ settings.rate_limit_per_second }} on each channel</dd></div>
          <div><dt class="muted">Most calls for one message</dt><dd data-testid="settings-attempts">{{ settings.max_attempts }}</dd></div>
          <div><dt class="muted">Provider</dt><dd>Mock provider. No real person gets a message.</dd></div>
        </dl>
      </section>

      <section class="section">
        <h2>Demo data</h2>
        <dl class="card rows">
          <div><dt class="muted">Customers</dt><dd data-testid="settings-customers">{{ settings.customers.toLocaleString("en-GB") }} fake customers</dd></div>
          <div><dt class="muted">Messages it sends</dt><dd><a :href="testInbox" target="_blank" rel="noopener" data-testid="settings-inbox">Open the test inbox</a></dd></div>
          <div><dt class="muted">To start over</dt><dd>Run <code class="mono">docker compose down -v</code>, then start the app again. This removes every campaign.</dd></div>
        </dl>
      </section>

    </template>
  </div>
</template>

<style scoped>
.settings,
.section,
.intro {
  display: grid;
  gap: var(--space-3);
  min-width: 0;
}

.settings {
  gap: var(--space-6);
  max-width: 760px;
}

h2 {
  margin: 0;
}

dl {
  margin: 0;
}

dd {
  margin: 0;
}

.rows > div {
  display: grid;
  grid-template-columns: minmax(0, 240px) minmax(0, 1fr);
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--color-border);
}

.rows > div:last-child {
  border-bottom: 0;
}

.section-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-3);
}

.checks {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-4);
}

.check {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 40px;
  cursor: pointer;
}

.select {
  justify-self: start;
  width: auto;
  height: 40px;
  padding-block: 0;
}

.saved {
  color: var(--color-success);
}

@media (max-width: 560px) {
  .rows > div {
    grid-template-columns: minmax(0, 1fr);
    gap: var(--space-1);
  }
}
</style>

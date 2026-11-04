<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useId, watch } from "vue";

import { previewEmail, type EmailPreviewResult, type Messages } from "../api/campaigns";
import { renderMessage } from "../lib/message";

const props = defineProps<{
  message: Partial<Messages>;
  recipients: { id: number; label: string; first_name: string }[];
  language?: string;
}>();

const MODES = [
  { id: "desktop", label: "Desktop", width: 640 },
  { id: "mobile", label: "Mobile", width: 375 },
  { id: "text", label: "Plain text", width: 0 },
] as const;
const PREVIEW_DELAY_MS = 250;

const id = useId();
const selectedId = ref(String(props.recipients[0]?.id ?? ""));
// The people change with the language shown and can arrive after the first render.
watch(
  () => props.recipients,
  (people) => {
    if (!people.some((person) => String(person.id) === selectedId.value)) selectedId.value = String(people[0]?.id ?? "");
  },
);
const firstName = computed(() => props.recipients.find((recipient) => String(recipient.id) === selectedId.value)?.first_name ?? "");
const values = computed(() => ({ first_name: firstName.value }));

const mode = ref<(typeof MODES)[number]["id"]>("desktop");
const designWidth = computed(() => MODES.find((item) => item.id === mode.value)?.width ?? 0);
const rendered = ref<EmailPreviewResult | null>(null);

// Rendered by the backend, so this is the template people actually receive.
let latest = 0;
let timer: ReturnType<typeof setTimeout> | undefined;
async function load() {
  const request = ++latest;
  try {
    const result = await previewEmail(props.message, firstName.value, props.language);
    if (request === latest) rendered.value = result.html ? result : null;
  } catch {
    if (request === latest) rendered.value = null;
  }
}
watch(
  () => [props.message, firstName.value],
  () => {
    clearTimeout(timer);
    timer = setTimeout(load, PREVIEW_DELAY_MS);
  },
  { deep: true },
);
// Another language has nothing to wait for, and its subject must not sit above the last body.
watch(
  () => props.language,
  () => {
    clearTimeout(timer);
    void load();
  },
);

// A design wider than the column is scaled down instead of reflowing into its
// mobile layout, which would make the two views look the same.
const frameBox = ref<HTMLElement | null>(null);
const available = ref(0);
const frameHeight = ref(480);
const scale = computed(() => (available.value && designWidth.value ? Math.min(1, available.value / designWidth.value) : 1));
let observer: ResizeObserver | undefined;

function measure(event: Event) {
  const body = (event.target as HTMLIFrameElement).contentDocument?.documentElement;
  if (body?.scrollHeight) frameHeight.value = body.scrollHeight;
}

onMounted(() => {
  void load();
  if (typeof ResizeObserver === "undefined" || !frameBox.value) return;
  observer = new ResizeObserver(([entry]) => {
    available.value = entry.contentRect.width;
  });
  observer.observe(frameBox.value);
});
onBeforeUnmount(() => {
  clearTimeout(timer);
  observer?.disconnect();
});
</script>

<template>
  <section class="preview" aria-label="Email preview">
    <div v-if="recipients.length" class="control">
      <label class="small muted" :for="id">Preview as</label>
      <select :id="id" v-model="selectedId" class="field select">
        <option v-for="recipient in recipients" :key="recipient.id" :value="String(recipient.id)">
          {{ recipient.label }}
        </option>
      </select>
    </div>

    <div class="modes" role="group" aria-label="Preview type">
      <button
        v-for="item in MODES"
        :key="item.id"
        type="button"
        class="mode small"
        :class="{ 'mode--on': mode === item.id }"
        :aria-pressed="mode === item.id"
        :data-testid="`email-mode-${item.id}`"
        @click="mode = item.id"
      >
        {{ item.label }}
      </button>
    </div>

    <div class="mail card">
      <div class="head">
        <p class="subject" data-testid="email-subject">{{ renderMessage(message.email_subject ?? "", values, language) }}</p>
        <p v-if="message.email_preheader" class="muted small preheader" data-testid="email-preheader">{{ renderMessage(message.email_preheader, values, language) }}</p>
      </div>
      <div ref="frameBox" class="box">
        <p v-if="!rendered" class="text" data-testid="email-body">{{ renderMessage(message.email_body ?? "", values, language) }}</p>
        <p v-else-if="mode === 'text'" class="text" data-testid="email-text">{{ rendered.text }}</p>
        <div v-else class="stage" :style="{ height: `${frameHeight * scale}px` }">
          <iframe
            class="frame"
            title="The email as it will look"
            sandbox="allow-same-origin"
            data-testid="email-frame"
            :srcdoc="rendered.html"
            :style="{ width: `${designWidth}px`, height: `${frameHeight}px`, transform: `scale(${scale})` }"
            @load="measure"
          ></iframe>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.preview {
  display: grid;
  gap: var(--space-3);
  min-width: 0;
}

.control {
  display: grid;
  gap: var(--space-1);
}

.select {
  height: 40px;
  padding-block: 0;
}

.modes {
  display: flex;
  gap: var(--space-1);
  width: fit-content;
  max-width: 100%;
  padding: 2px;
  background: var(--color-surface-subtle);
  border-radius: var(--radius-md);
}

.mode {
  min-height: 32px;
  padding: 0 var(--space-3);
  background: transparent;
  border: 0;
  border-radius: var(--radius-sm);
  color: var(--color-text-muted);
  cursor: pointer;
}

.mode--on {
  background: var(--color-surface);
  color: var(--color-text);
  font-weight: 500;
  box-shadow: 0 0 0 1px var(--color-border);
}

.mail {
  display: grid;
  min-width: 0;
  overflow: hidden;
}

.head {
  display: grid;
  gap: 2px;
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--color-border);
}

.subject {
  font-weight: 600;
  overflow-wrap: anywhere;
}

.preheader {
  overflow-wrap: anywhere;
}

.box {
  min-width: 0;
  overflow: hidden;
}

.text {
  padding: var(--space-4);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.stage {
  overflow: hidden;
}

.frame {
  display: block;
  margin: 0 auto;
  border: 0;
  transform-origin: top left;
}
</style>

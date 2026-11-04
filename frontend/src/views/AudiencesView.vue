<script setup lang="ts">
import { onMounted, ref } from "vue";

import { deleteAudience, errorMessage, getAudiences, recountAudience, type SavedAudience } from "../api/campaigns";
import ValidationNotice from "../components/ValidationNotice.vue";
import { ruleText } from "../lib/audience";
import { formatDateTime } from "../lib/format";

const audiences = ref<SavedAudience[] | null>(null);
const error = ref("");
const busyId = ref("");
const confirmingId = ref("");
const recounted = ref<{ id: string; text: string } | null>(null);

const count = (value: number) => value.toLocaleString("en-GB");

onMounted(async () => {
  try {
    audiences.value = await getAudiences();
  } catch (failure) {
    error.value = errorMessage(failure);
  }
});

// The numbers often stay the same, so say that it counted instead of looking like nothing happened.
function whatChanged(before: SavedAudience | undefined, after: SavedAudience) {
  const rows: [string, number | undefined, number][] = [
    ["customers", before?.audience.total, after.audience.total],
    ["by SMS", before?.audience.sms, after.audience.sms],
    ["by email", before?.audience.email, after.audience.email],
  ];
  const changed = rows.filter(([, was, now]) => was !== now).map(([label, was, now]) => `${label} ${count(was ?? 0)} to ${count(now)}`);
  return changed.length ? `Counted again. Changed: ${changed.join(", ")}.` : `Counted again. Still ${count(after.audience.total)} customers, nothing changed.`;
}

async function recount(id: string) {
  busyId.value = id;
  error.value = "";
  recounted.value = null;
  try {
    const before = audiences.value?.find((item) => item.id === id);
    const fresh = await recountAudience(id);
    audiences.value = (audiences.value ?? []).map((item) => (item.id === id ? fresh : item));
    recounted.value = { id, text: whatChanged(before, fresh) };
  } catch (failure) {
    error.value = errorMessage(failure);
  } finally {
    busyId.value = "";
  }
}

async function remove(id: string) {
  busyId.value = id;
  error.value = "";
  try {
    await deleteAudience(id);
    audiences.value = (audiences.value ?? []).filter((item) => item.id !== id);
  } catch (failure) {
    error.value = errorMessage(failure);
  } finally {
    busyId.value = "";
    confirmingId.value = "";
  }
}
</script>

<template>
  <div class="audiences">
    <header class="intro">
      <h1>Keep the audiences you use again.</h1>
      <p class="muted">A saved audience is a rule, and its numbers are from the last count. A campaign counts its own audience when it is written and fixes it when approved.</p>
    </header>

    <ValidationNotice v-if="error" tone="error" title="That did not work">{{ error }}</ValidationNotice>
    <p v-if="!audiences && !error" class="muted" role="status">Loading audiences...</p>

    <div v-else-if="audiences && !audiences.length" class="empty card" data-testid="no-audiences">
      <p class="muted">No saved audiences yet. Save one from a campaign's Audience step, once its rule is ready.</p>
      <div><a class="button button--primary link-button" href="/campaigns/new">New campaign</a></div>
    </div>

    <section v-else-if="audiences" class="list" aria-label="Saved audiences">
      <article v-for="item in audiences" :key="item.id" class="audience card" data-testid="audience">
        <div class="head">
          <h2>{{ item.name }}</h2>
          <p class="small muted" data-testid="audience-counted">Counted {{ formatDateTime(item.counted_at) }}</p>
        </div>
        <p class="rules">
          <template v-for="(condition, index) in item.conditions" :key="condition.field">
            <span v-if="index" class="muted small">AND</span>
            <span class="chip small">{{ ruleText(condition) }}</span>
          </template>
        </p>
        <dl class="numbers">
          <div><dt class="small muted">Matching customers</dt><dd class="mono" data-testid="audience-total">{{ count(item.audience.total) }}</dd></div>
          <div><dt class="small muted">Reachable by SMS</dt><dd class="mono" data-testid="audience-sms">{{ count(item.audience.sms) }}</dd></div>
          <div><dt class="small muted">Reachable by email</dt><dd class="mono" data-testid="audience-email">{{ count(item.audience.email) }}</dd></div>
        </dl>
        <p v-if="item.source_campaign_id" class="small muted">Saved from <a :href="`/campaigns/${item.source_campaign_id}`">{{ item.source_campaign_name }}</a></p>
        <p v-if="recounted?.id === item.id" class="small recounted" role="status" data-testid="audience-recounted">{{ recounted.text }}</p>
        <div class="actions">
          <a class="button button--secondary link-button" :href="`/campaigns/new?audience=${item.id}`" data-testid="use-audience">Use in a new campaign</a>
          <button class="button button--secondary" type="button" :disabled="busyId === item.id" data-testid="recount-audience" @click="recount(item.id)">{{ busyId === item.id ? "Counting..." : "Count again" }}</button>
          <button v-if="confirmingId !== item.id" class="link small remove" type="button" data-testid="delete-audience" @click="confirmingId = item.id">Delete</button>
          <template v-else>
            <button class="link small remove" type="button" :disabled="busyId === item.id" data-testid="confirm-delete-audience" @click="remove(item.id)">Yes, delete it</button>
            <button class="link small" type="button" @click="confirmingId = ''">Keep it</button>
          </template>
        </div>
      </article>
    </section>
  </div>
</template>

<style scoped>
.audiences,
.intro,
.list {
  display: grid;
  gap: var(--space-3);
  min-width: 0;
}

.audiences {
  gap: var(--space-6);
}

.audience {
  display: grid;
  gap: var(--space-3);
  min-width: 0;
  padding: var(--space-4);
}

.head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-2);
}

h2,
p {
  margin: 0;
}

h2 {
  font-size: inherit;
  font-weight: 600;
}

.rules {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.chip {
  padding: 2px var(--space-2);
  background: var(--color-primary-soft);
  border-radius: var(--radius-sm);
}

.numbers {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: var(--space-3);
  margin: 0;
}

dd {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
}

.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
}

.recounted {
  color: var(--color-success);
}

.link-button {
  display: inline-flex;
  text-decoration: none;
}

.link {
  padding: 0;
  color: var(--color-primary);
  background: transparent;
  border: 0;
  font: inherit;
  cursor: pointer;
}

.remove {
  color: var(--color-error);
}

.empty {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-6);
}
</style>

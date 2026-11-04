<script setup lang="ts">
import { computed } from "vue";

import type { Campaign } from "../api/campaigns";
import { partRules } from "../lib/audience";

const props = defineProps<{ campaign: Campaign }>();

const parts = computed(() => props.campaign.proposal?.brief_parts ?? []);
const forAudience = computed(() => parts.value.filter((part) => part.kind === "audience"));
const notForAudience = computed(() => parts.value.filter((part) => part.kind !== "audience"));
const used = computed(() => forAudience.value.filter((part) => part.used_as?.length).length);
</script>

<template>
  <section v-if="parts.length" class="reading card" data-testid="brief-reading">
    <div class="head">
      <h2>How the brief was read</h2>
      <span class="small count" :class="{ 'count--missing': used < forAudience.length }" data-testid="parts-used">
        {{ used }} of {{ forAudience.length }} audience parts used
      </span>
    </div>
    <table v-if="forAudience.length">
      <thead>
        <tr>
          <th scope="col">In the brief</th>
          <th scope="col">In the rule</th>
          <th scope="col">Result</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="part in forAudience" :key="part.text" :class="{ missing: !part.used_as?.length }" data-testid="audience-part">
          <td>{{ part.text }}</td>
          <td>
            <div v-if="part.used_as?.length" class="rule-chips">
              <span v-for="rule in partRules(campaign, part)" :key="rule" class="rule-chip">{{ rule }}</span>
            </div>
            <span v-else class="muted">Nothing</span>
          </td>
          <td class="result">{{ part.used_as?.length ? "Used" : "Not used, no filter for this" }}</td>
        </tr>
      </tbody>
    </table>
    <div v-if="notForAudience.length" class="others">
      <h3>Not used for the audience</h3>
      <ul class="small" data-testid="other-parts">
        <li v-for="part in notForAudience" :key="part.text">{{ part.text }}</li>
      </ul>
    </div>
  </section>
</template>

<style scoped>
.reading {
  display: grid;
  gap: var(--space-4);
  min-width: 0;
  padding: var(--space-4);
}

.head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-2) var(--space-4);
}

h2,
h3,
ul {
  margin: 0;
}

h2,
h3 {
  font-size: inherit;
  font-weight: 600;
}

.count {
  color: var(--color-success);
  font-weight: 500;
}

.count--missing {
  color: var(--color-error);
}

table {
  width: 100%;
  border-collapse: collapse;
}

th {
  padding: 0 var(--space-3) var(--space-2) 0;
  color: var(--color-text-muted);
  font-size: 12px;
  font-weight: 500;
  text-align: left;
}

td {
  padding: var(--space-3) var(--space-3) var(--space-3) 0;
  border-top: 1px solid var(--color-border);
  vertical-align: top;
}

.result {
  color: var(--color-success);
  font-weight: 500;
}

.missing td {
  background: color-mix(in srgb, var(--color-error) 7%, transparent);
}

.missing td:first-child {
  padding-left: var(--space-2);
}

.missing .result {
  color: var(--color-error);
}

.rule-chips {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.rule-chip {
  display: inline-block;
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm);
  background: var(--color-surface-subtle);
  font-weight: 500;
}

.others {
  display: grid;
  gap: var(--space-2);
}

ul {
  padding-left: var(--space-4);
}

@media (max-width: 640px) {
  table,
  tbody,
  tr,
  td {
    display: block;
  }

  thead {
    display: none;
  }

  tr {
    padding: var(--space-2) 0;
    border-top: 1px solid var(--color-border);
  }

  td {
    padding: var(--space-1) var(--space-2);
    border-top: 0;
  }
}
</style>

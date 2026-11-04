<script setup lang="ts">
import type { RuleChange } from "../lib/audience";

defineProps<{ changes: RuleChange[]; before: number; after: number }>();

const count = (value: number) => value.toLocaleString("en-GB");
</script>

<template>
  <section class="diff card" data-testid="refinement-diff">
    <h3>What your request changed</h3>
    <ul class="rows">
      <li v-for="change in changes" :key="change.field" class="row" data-testid="refinement-change">
        <span class="kind small muted">{{ !change.before ? "Added" : !change.after ? "Removed" : "Changed" }}</span>
        <span>
          <del v-if="change.before" class="muted">{{ change.before }}</del>
          <span v-if="change.before && change.after" class="muted" aria-hidden="true"> → </span>
          <ins v-if="change.after">{{ change.after }}</ins>
        </span>
      </li>
      <li class="row" data-testid="refinement-audience">
        <span class="kind small muted">Audience</span>
        <span v-if="before === after" class="mono">{{ count(after) }}, unchanged</span>
        <span v-else class="mono"><del class="muted">{{ count(before) }}</del><span class="muted" aria-hidden="true"> → </span><ins>{{ count(after) }}</ins></span>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.diff {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-4);
}

h3 {
  margin: 0;
  font-size: inherit;
  font-weight: 600;
}

.rows {
  display: grid;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.row {
  display: grid;
  grid-template-columns: 80px minmax(0, 1fr);
  gap: var(--space-3);
  align-items: baseline;
}

ins {
  font-weight: 500;
  text-decoration: none;
}
</style>

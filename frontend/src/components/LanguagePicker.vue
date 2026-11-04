<script setup lang="ts">
import { computed } from "vue";

import type { Campaign } from "../api/campaigns";
import { audienceLanguages, languageChoices, listNames, languageName } from "../lib/languages";

const props = defineProps<{ campaign: Campaign; disabled?: boolean }>();
const emit = defineEmits<{ update: [excluded: string[]] }>();

const choices = computed(() => languageChoices(props.campaign));
// One language, English, leaves nothing to choose.
const shown = computed(() => choices.value.some((choice) => !choice.locked));
const english = computed(() => audienceLanguages(props.campaign).find((version) => version.language === "en"));
const fallsBack = computed(() => english.value?.also ?? []);
// Nobody in this audience reads English, so it appears only once someone needs it.
const englishOnlyForOthers = computed(() => fallsBack.value.length > 0 && !choices.value.some((choice) => choice.locked));

function toggle(language: string, chosen: boolean) {
  const excluded = new Set(props.campaign.excluded_languages ?? []);
  if (chosen) excluded.delete(language);
  else excluded.add(language);
  emit("update", [...excluded].sort());
}

function people(count: number) {
  return `${count.toLocaleString("en-US")} ${count === 1 ? "person" : "people"}`;
}
</script>

<template>
  <section v-if="shown" class="section" data-testid="language-picker">
    <h2>Message languages</h2>
    <p class="muted small">This audience reads these languages. Choose which get their own messages. It does not change who receives this: anyone else gets English.</p>
    <fieldset class="picker card" :disabled="disabled">
      <legend class="visually-hidden">Message languages</legend>
      <label v-for="choice in choices" :key="choice.language" class="option">
        <input
          type="checkbox"
          :checked="choice.chosen"
          :disabled="choice.locked"
          :data-testid="`language-choice-${choice.language}`"
          @change="toggle(choice.language, ($event.target as HTMLInputElement).checked)"
        />
        <span class="name">{{ choice.name }}</span>
        <span class="muted small count" :data-testid="`language-count-${choice.language}`">{{ people(choice.people) }}</span>
        <span v-if="choice.locked" class="muted small note">Always written, for anyone whose language is not chosen</span>
        <span v-else-if="!choice.chosen" class="muted small note">Gets English</span>
      </label>
      <div v-if="englishOnlyForOthers" class="option" data-testid="language-english-added">
        <input type="checkbox" checked disabled aria-label="English" />
        <span class="name">{{ languageName("en") }}</span>
        <span class="muted small count">{{ people(english?.total ?? 0) }}</span>
        <span class="muted small note">Added for those who read {{ listNames(fallsBack.map(languageName)) }}</span>
      </div>
    </fieldset>
    <p v-if="campaign.content" class="muted small" data-testid="languages-keep-content">Leaving one out removes its messages. Adding one writes only that language, from the message steps.</p>
  </section>
</template>

<style scoped>
.picker {
  display: grid;
  gap: 0;
  min-width: 0;
  margin: 0;
  padding: 0;
}

.option {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-2);
  padding: var(--space-3);
  cursor: pointer;
}

.option + .option {
  border-top: 1px solid var(--color-border);
}

.picker:disabled .option {
  cursor: not-allowed;
  opacity: 0.6;
}

.option input {
  margin: 0;
  accent-color: var(--color-primary);
}

.name {
  font-weight: 500;
}

.count {
  font-variant-numeric: tabular-nums;
}

.note {
  margin-left: auto;
}

.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
}
</style>

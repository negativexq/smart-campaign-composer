<script setup lang="ts">
import { computed, nextTick, ref } from "vue";

import { errorMessage, getTags, setTags, type Campaign, type TagUse } from "../api/campaigns";

const props = defineProps<{ campaignId: string; tags: string[] }>();
const emit = defineEmits<{ updated: [campaign: Campaign] }>();

const MAX_TAGS = 10;
const MAX_LENGTH = 30;

const adding = ref(false);
const draft = ref("");
const saving = ref(false);
const problem = ref("");
const known = ref<TagUse[]>([]);
const input = ref<HTMLInputElement | null>(null);

// Tags other campaigns use, so the same word is picked rather than typed a new way.
const suggestions = computed(() => known.value.filter((use) => !props.tags.some((tag) => tag.toLowerCase() === use.tag.toLowerCase())));

async function save(tags: string[]) {
  saving.value = true;
  problem.value = "";
  try {
    emit("updated", await setTags(props.campaignId, tags));
    return true;
  } catch (failure) {
    problem.value = errorMessage(failure);
    return false;
  } finally {
    saving.value = false;
  }
}

async function open() {
  adding.value = true;
  problem.value = "";
  getTags().then((tags) => (known.value = tags), () => {});
  await nextTick();
  input.value?.focus();
}

async function add() {
  const tag = draft.value.trim().replace(/\s+/g, " ");
  if (!tag) {
    adding.value = false;
    return;
  }
  if (tag.length > MAX_LENGTH) {
    problem.value = `Keep a tag to ${MAX_LENGTH} characters.`;
    return;
  }
  if (await save([...props.tags, tag])) {
    draft.value = "";
    adding.value = false;
  }
}

function cancel() {
  draft.value = "";
  adding.value = false;
  problem.value = "";
}
</script>

<template>
  <div class="tags" data-testid="tag-editor">
    <ul class="list" aria-label="Tags">
      <li v-for="tag in tags" :key="tag" class="tag" data-testid="tag">
        <a :href="`/campaigns?tag=${encodeURIComponent(tag)}`" class="tag-link">{{ tag }}</a>
        <button type="button" class="remove" :aria-label="`Remove tag ${tag}`" :disabled="saving" @click="save(tags.filter((other) => other !== tag))">×</button>
      </li>
    </ul>
    <form v-if="adding" class="add" @submit.prevent="add">
      <label class="visually-hidden" for="new-tag">New tag</label>
      <input
        id="new-tag"
        ref="input"
        v-model="draft"
        class="field input"
        list="known-tags"
        :maxlength="MAX_LENGTH"
        placeholder="Tag name"
        autocomplete="off"
        data-testid="tag-input"
        :disabled="saving"
        @keydown.esc.prevent="cancel"
        @blur="!draft.trim() && cancel()"
      />
      <datalist id="known-tags">
        <option v-for="use in suggestions" :key="use.tag" :value="use.tag" />
      </datalist>
      <button class="button button--secondary small-button" type="submit" data-testid="tag-save" :disabled="saving">Add</button>
    </form>
    <button v-else-if="tags.length < MAX_TAGS" type="button" class="add-button" data-testid="add-tag" @click="open">+ Add tag</button>
    <p v-if="problem" class="error small" role="alert">{{ problem }}</p>
  </div>
</template>

<style scoped>
.tags {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.list {
  display: contents;
  margin: 0;
  padding: 0;
  list-style: none;
}

.tag {
  display: inline-flex;
  align-items: center;
  height: 26px;
  padding: 0 var(--space-1) 0 var(--space-2);
  background: var(--color-surface-subtle);
  border: 1px solid var(--color-border);
  border-radius: 999px;
  font-size: 13px;
}

.tag-link {
  color: var(--color-text);
  text-decoration: none;
}

.tag-link:hover {
  color: var(--color-primary);
}

.remove {
  display: inline-grid;
  place-items: center;
  width: 20px;
  height: 20px;
  margin-left: 2px;
  padding: 0;
  color: var(--color-text-muted);
  background: none;
  border: 0;
  border-radius: 999px;
  font: inherit;
  cursor: pointer;
}

.remove:hover:not(:disabled) {
  color: var(--color-text);
  background: var(--color-border);
}

.add {
  display: inline-flex;
  gap: var(--space-1);
}

.input {
  width: 160px;
  height: 30px;
  padding-block: 0;
  font-size: 13px;
}

.small-button {
  min-height: 30px;
  padding: 0 var(--space-3);
  font-size: 13px;
}

.add-button {
  height: 26px;
  padding: 0 var(--space-2);
  color: var(--color-text-muted);
  background: none;
  border: 1px dashed var(--color-border-input);
  border-radius: 999px;
  font: inherit;
  font-size: 13px;
  cursor: pointer;
}

.add-button:hover {
  color: var(--color-primary);
  border-color: var(--color-primary);
}

.error {
  flex-basis: 100%;
  margin: 0;
  color: var(--color-error);
}
</style>

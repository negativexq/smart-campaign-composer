<script setup lang="ts">
import { nextTick, ref } from "vue";

import { errorMessage, renameCampaign, type Campaign } from "../api/campaigns";
import OriginTag from "./OriginTag.vue";

const props = defineProps<{ as: "h1" | "h2"; campaign: Campaign }>();
const emit = defineEmits<{ renamed: [campaign: Campaign] }>();

const editing = ref(false);
const draft = ref("");
const saving = ref(false);
const error = ref("");
const input = ref<HTMLInputElement | null>(null);

async function start() {
  draft.value = props.campaign.name;
  error.value = "";
  editing.value = true;
  await nextTick();
  input.value?.select();
}

async function save() {
  saving.value = true;
  error.value = "";
  try {
    emit("renamed", await renameCampaign(props.campaign.id, draft.value));
    editing.value = false;
  } catch (failure) {
    error.value = errorMessage(failure);
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <div class="name" data-testid="campaign-name">
    <form v-if="editing" class="edit" @submit.prevent="save">
      <label class="visually-hidden" for="campaign-name-input">Campaign name</label>
      <input id="campaign-name-input" ref="input" v-model="draft" class="field" />
      <button class="button button--primary" type="submit" :disabled="saving || !draft.trim()">Save</button>
      <button class="button button--secondary" type="button" :disabled="saving" @click="editing = false">Cancel</button>
      <p v-if="error" class="error small" role="alert">{{ error }}</p>
    </form>
    <div v-else class="show">
      <component :is="as" class="text">{{ campaign.name }}</component>
      <OriginTag v-if="campaign.name_source === 'ai'" origin="ai" />
      <button class="rename small" type="button" data-testid="rename" @click="start">Rename</button>
    </div>
  </div>
</template>

<style scoped>
.name {
  width: 100%;
  min-width: 0;
}

.show,
.edit {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-3);
}

.text {
  min-width: 0;
  overflow-wrap: anywhere;
}

.edit .field {
  flex: 1;
  min-width: 200px;
}

.rename {
  padding: 0;
  color: var(--color-primary);
  background: transparent;
  border: 0;
  font: inherit;
  cursor: pointer;
}

.rename:hover {
  text-decoration: underline;
}

.error {
  flex-basis: 100%;
  margin: 0;
  color: var(--color-error);
}
</style>

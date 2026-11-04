<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import { errorMessage, getBrand, saveBrand, type Brand } from "../api/campaigns";
import AssetLibrary from "../components/AssetLibrary.vue";
import ValidationNotice from "../components/ValidationNotice.vue";

const HEX = /^#[0-9A-Fa-f]{6}$/;
const brand = ref<Brand | null>(null);
const error = ref("");
const saving = ref(false);
const saved = ref(false);
const choosing = ref(false);

const colorsValid = computed(() => Boolean(brand.value && HEX.test(brand.value.accent) && HEX.test(brand.value.page)));
const logoUrl = computed(() => (brand.value?.logo_asset_id ? `/api/assets/${brand.value.logo_asset_id}/file` : ""));

async function save() {
  if (!brand.value) return;
  saving.value = true;
  saved.value = false;
  error.value = "";
  try {
    brand.value = await saveBrand(brand.value);
    saved.value = true;
  } catch (failure) {
    error.value = errorMessage(failure);
  } finally {
    saving.value = false;
  }
}

onMounted(async () => {
  try {
    brand.value = await getBrand();
  } catch (failure) {
    error.value = errorMessage(failure);
  }
});
</script>

<template>
  <div class="brand">
    <header class="intro">
      <h1>Set your brand once.</h1>
      <p class="muted">New email templates start with this logo, these colors and these footer details. Templates you already made keep their look.</p>
    </header>
    <ValidationNotice v-if="error" tone="error" title="The brand kit was not saved">{{ error }}</ValidationNotice>
    <p v-if="!brand && !error" class="muted" role="status">Loading the brand kit...</p>

    <form v-if="brand" class="form card" data-testid="brand-form" @submit.prevent="save">
      <div class="field-group">
        <label for="brand-name">Brand name</label>
        <input id="brand-name" v-model="brand.name" class="field" maxlength="60" data-testid="brand-name" />
      </div>

      <div class="field-group">
        <span class="label">Logo</span>
        <div class="logo-row">
          <div class="logo-box">
            <img v-if="logoUrl" :src="logoUrl" :alt="brand.name || 'Logo'" data-testid="brand-logo" />
            <span v-else class="small muted">No logo</span>
          </div>
          <button class="button button--secondary" type="button" data-testid="brand-choose-logo" @click="choosing = !choosing">{{ choosing ? "Close" : brand.logo_asset_id ? "Change logo" : "Choose logo" }}</button>
          <button v-if="brand.logo_asset_id" class="plain small" type="button" data-testid="brand-remove-logo" @click="brand.logo_asset_id = ''">Remove</button>
        </div>
        <AssetLibrary v-if="choosing" :selected="brand.logo_asset_id" @choose="(asset) => { brand!.logo_asset_id = asset.id; choosing = false; }" />
      </div>

      <div class="columns">
        <div class="field-group">
          <label for="brand-accent">Main color</label>
          <div class="color">
            <input :value="HEX.test(brand.accent) ? brand.accent : '#000000'" class="picker" type="color" aria-label="Pick the main color" @input="brand!.accent = ($event.target as HTMLInputElement).value.toUpperCase()" />
            <input id="brand-accent" v-model="brand.accent" class="field mono" maxlength="7" data-testid="brand-accent" />
          </div>
        </div>
        <div class="field-group">
          <label for="brand-page">Background</label>
          <div class="color">
            <input :value="HEX.test(brand.page) ? brand.page : '#000000'" class="picker" type="color" aria-label="Pick the background color" @input="brand!.page = ($event.target as HTMLInputElement).value.toUpperCase()" />
            <input id="brand-page" v-model="brand.page" class="field mono" maxlength="7" data-testid="brand-page" />
          </div>
        </div>
        <div class="field-group">
          <label for="brand-button">Button shape</label>
          <select id="brand-button" v-model="brand.button" class="field">
            <option value="square">Square</option>
            <option value="rounded">Rounded</option>
            <option value="pill">Pill</option>
          </select>
        </div>
        <div class="field-group">
          <label for="brand-font">Font</label>
          <select id="brand-font" v-model="brand.font" class="field">
            <option value="sans">Clean (sans serif)</option>
            <option value="serif">Classic (serif)</option>
          </select>
        </div>
      </div>
      <p v-if="!colorsValid" class="small problem" role="alert">Colors must look like #315BE8.</p>

      <div class="field-group">
        <label for="brand-footer">Footer details <span class="muted small">Optional</span></label>
        <input id="brand-footer" v-model="brand.footer" class="field" maxlength="200" placeholder="Company name, address" data-testid="brand-footer" />
        <span class="small muted">Shown above the unsubscribe link, which every email always has.</span>
      </div>

      <div class="actions">
        <button class="button button--primary" type="submit" :disabled="saving || !colorsValid" data-testid="brand-save">{{ saving ? "Saving..." : "Save brand kit" }}</button>
        <span v-if="saved" class="small muted" role="status" data-testid="brand-saved">Saved. New templates will start from this.</span>
      </div>
    </form>
  </div>
</template>

<style scoped>
.brand,
.intro,
.form,
.field-group {
  display: grid;
  gap: var(--space-2);
  min-width: 0;
}

.brand {
  gap: var(--space-6);
}

.intro p {
  margin: 0;
  max-width: 720px;
}

.form {
  gap: var(--space-5, 20px);
  max-width: 720px;
  padding: var(--space-6);
}

label,
.label {
  font-weight: 500;
}

.logo-row,
.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
}

.logo-box {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 160px;
  height: 72px;
  padding: var(--space-2);
  background: var(--color-surface-subtle);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
}

.logo-box img {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
}

.columns {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: var(--space-3);
}

.color {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.color .field {
  min-width: 0;
}

.picker {
  flex: none;
  width: 40px;
  height: 40px;
  padding: 2px;
  background: var(--color-surface);
  border: 1px solid var(--color-border-input);
  border-radius: var(--radius-sm);
  cursor: pointer;
}

.problem {
  margin: 0;
  color: var(--color-error);
}

.plain {
  padding: 0;
  color: var(--color-text-muted);
  background: none;
  border: 0;
  font: inherit;
  text-decoration: underline;
  cursor: pointer;
}
</style>

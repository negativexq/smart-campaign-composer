<script setup lang="ts">
import { onMounted, ref } from "vue";

import { deleteAsset, errorMessage, getAssets, uploadAsset, type Asset } from "../api/campaigns";

const MAX_BYTES = 5 * 1024 * 1024;

const props = defineProps<{ selected?: string; manage?: boolean }>();
const emit = defineEmits<{ choose: [asset: Asset] }>();

const assets = ref<Asset[] | null>(null);
const error = ref("");
const uploading = ref(false);
const confirming = ref("");

const size = (bytes: number) => (bytes < 1024 * 1024 ? `${Math.max(1, Math.round(bytes / 1024))} KB` : `${(bytes / (1024 * 1024)).toFixed(1)} MB`);

async function upload(event: Event) {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = "";
  if (!file) return;
  error.value = "";
  // The server checks again by content; this only saves a long wait for a sure refusal.
  if (file.size > MAX_BYTES) {
    error.value = "Choose an image of up to 5 MB.";
    return;
  }
  uploading.value = true;
  try {
    const saved = await uploadAsset(file);
    assets.value = [saved, ...(assets.value ?? []).filter((item) => item.id !== saved.id)];
    if (!props.manage) emit("choose", saved);
  } catch (failure) {
    error.value = errorMessage(failure);
  } finally {
    uploading.value = false;
  }
}

async function remove(id: string) {
  error.value = "";
  try {
    await deleteAsset(id);
    assets.value = (assets.value ?? []).filter((item) => item.id !== id);
  } catch (failure) {
    error.value = errorMessage(failure);
  }
  confirming.value = "";
}

onMounted(async () => {
  try {
    assets.value = await getAssets();
  } catch (failure) {
    error.value = errorMessage(failure);
  }
});
</script>

<template>
  <div class="library" data-testid="asset-library">
    <div class="head">
      <label class="button button--secondary upload" :class="{ 'upload--busy': uploading }">
        <input class="visually-hidden" type="file" accept="image/png,image/jpeg" :disabled="uploading" data-testid="asset-upload" @change="upload" />
        {{ uploading ? "Uploading..." : "Upload image" }}
      </label>
      <span class="small muted">PNG or JPEG, up to 5 MB</span>
    </div>
    <p v-if="error" class="small problem" role="alert" data-testid="asset-error">{{ error }}</p>
    <p v-if="!assets && !error" class="small muted" role="status">Loading images...</p>
    <p v-else-if="assets && !assets.length" class="small muted" data-testid="no-assets">No images yet. Upload your logo or a campaign picture.</p>
    <ul v-else-if="assets" class="grid" :class="{ 'grid--wide': manage }">
      <li v-for="asset in assets" :key="asset.id" class="item" :data-testid="`asset-${asset.id}`">
        <button v-if="!manage" class="thumb" :class="{ 'thumb--on': selected === asset.id }" type="button" :aria-pressed="selected === asset.id" :aria-label="`Use ${asset.name}`" data-testid="asset-choose" @click="emit('choose', asset)">
          <img :src="asset.url" alt="" loading="lazy" />
        </button>
        <template v-else>
          <div class="thumb thumb--still"><img :src="asset.url" alt="" loading="lazy" /></div>
          <p class="small name" :title="asset.name">{{ asset.name }}</p>
          <p class="small muted">{{ asset.width }} × {{ asset.height }} · {{ size(asset.bytes) }}</p>
          <p v-if="asset.in_use" class="small muted" data-testid="asset-in-use">In use</p>
          <div v-else-if="confirming === asset.id" class="row">
            <button class="plain small danger" type="button" data-testid="asset-confirm-delete" @click="remove(asset.id)">Delete</button>
            <button class="plain small" type="button" @click="confirming = ''">Keep it</button>
          </div>
          <button v-else class="plain small" type="button" data-testid="asset-delete" @click="confirming = asset.id">Delete</button>
        </template>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.library {
  display: grid;
  gap: var(--space-3);
  min-width: 0;
}

.head,
.row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-3);
}

.upload {
  display: inline-flex;
  align-items: center;
  cursor: pointer;
}

.upload:has(input:focus-visible) {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.upload--busy {
  opacity: 0.6;
  cursor: progress;
}

.problem {
  margin: 0;
  color: var(--color-error);
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(72px, 1fr));
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.grid--wide {
  grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
  gap: var(--space-4);
}

.item {
  display: grid;
  align-content: start;
  gap: var(--space-1);
  min-width: 0;
}

.item p {
  margin: 0;
}

.name {
  overflow: hidden;
  font-weight: 500;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.thumb {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  aspect-ratio: 1;
  padding: 4px;
  overflow: hidden;
  background: var(--color-surface-subtle);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  cursor: pointer;
}

.thumb--still {
  aspect-ratio: 4 / 3;
  cursor: default;
}

.thumb--on {
  border-color: var(--color-primary);
  box-shadow: 0 0 0 1px var(--color-primary);
}

.thumb img {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
}

.plain {
  justify-self: start;
  padding: 0;
  color: var(--color-text-muted);
  background: none;
  border: 0;
  font: inherit;
  text-decoration: underline;
  cursor: pointer;
}

.danger {
  color: var(--color-error);
  font-weight: 500;
}

.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
}
</style>

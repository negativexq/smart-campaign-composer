<script setup lang="ts">
import AssetLibrary from "../components/AssetLibrary.vue";
</script>

<template>
  <div class="assets">
    <header class="intro">
      <h1>Your images, in one place.</h1>
      <p class="muted">Logos, banners and product pictures for your email templates. Each upload is checked and saved again without its hidden file details. An image in use cannot be deleted, so sent and approved campaigns keep theirs.</p>
    </header>
    <AssetLibrary manage />
  </div>
</template>

<style scoped>
.assets,
.intro {
  display: grid;
  gap: var(--space-2);
}

.assets {
  gap: var(--space-6);
}

.intro p {
  margin: 0;
  max-width: 720px;
}
</style>

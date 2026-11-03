<script setup lang="ts">
defineProps<{ message: string }>();
</script>

<template>
  <section class="loading card" role="status" data-testid="audience-loading">
    <span class="spinner" aria-hidden="true"></span>
    <div>
      <p class="title">{{ message }}</p>
      <p class="muted small">This can take a few seconds.</p>
    </div>
  </section>
</template>

<style scoped>
.loading {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-4);
}

p {
  margin: 0;
}

.title {
  font-weight: 600;
}

.spinner {
  flex-shrink: 0;
  width: 20px;
  height: 20px;
  border: 2px solid var(--color-primary-soft);
  border-top-color: var(--color-primary);
  border-radius: 50%;
  animation: spin 800ms linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

@media (prefers-reduced-motion: reduce) {
  .spinner {
    animation-duration: 2400ms;
  }
}
</style>

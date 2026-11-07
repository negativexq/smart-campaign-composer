<script setup lang="ts">
import { Mail, MessageSquareText } from "lucide-vue-next";

import { formatChannel } from "../lib/format";

// labels shows the channel's name beside its icon; without it the name is only read aloud.
defineProps<{ channels: string[]; labels?: boolean }>();
</script>

<template>
  <span class="channels" data-testid="channel-icons">
    <span v-for="channel in channels" :key="channel" class="channel" :title="formatChannel(channel)">
      <MessageSquareText v-if="channel === 'sms'" :size="14" aria-hidden="true" />
      <Mail v-else :size="14" aria-hidden="true" />
      <span :class="labels ? 'channel-label' : 'visually-hidden'">{{ formatChannel(channel) }}</span>
    </span>
  </span>
</template>

<style scoped>
.channels {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--color-text-muted);
}

.channel {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
}

.channel-label {
  font-size: 13px;
}
</style>

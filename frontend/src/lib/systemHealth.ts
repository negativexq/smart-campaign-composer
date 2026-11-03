import { computed, ref } from "vue";

import { fetchSystemHealth, type SystemHealth } from "../api/system";

export const SYSTEM_COMPONENTS = [
  { id: "database", label: "Database", effect: "Campaigns cannot be read or changed." },
  { id: "redis", label: "Rate limiter", effect: "Messages wait, because nothing is sent without the rate limit." },
  { id: "worker", label: "Sending worker", effect: "Messages wait in the queue. Anything being sent is picked up again within a minute of its return." },
  { id: "provider", label: "Mock provider", effect: "Messages wait. Calls that could not connect do not use up an attempt." },
];

export const systemHealth = ref<SystemHealth | null>(null);

export async function refreshSystemHealth() {
  systemHealth.value = await fetchSystemHealth();
}

// What is stopping messages from going out right now.
export const sendingBlockers = computed(() =>
  SYSTEM_COMPONENTS.filter((component) => component.id !== "database" && systemHealth.value?.checks[component.id] === "down"),
);

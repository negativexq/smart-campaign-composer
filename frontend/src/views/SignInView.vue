<script setup lang="ts">
import { Activity, MessageSquareText, PenLine, ShieldCheck, Users } from "lucide-vue-next";
import { ref } from "vue";

import { createSession, errorMessage } from "../api/session";
import ValidationNotice from "../components/ValidationNotice.vue";
import { currentUser } from "../lib/session";

const steps = [
  { icon: PenLine, title: "Brief", text: "Say who you want to reach, in your own words." },
  { icon: Users, title: "Audience", text: "Check the conditions and the real number of people." },
  { icon: MessageSquareText, title: "Content", text: "Edit the SMS and email within their limits." },
  { icon: ShieldCheck, title: "Your approval", text: "Nothing goes out until you approve it.", decision: true },
  { icon: Activity, title: "Monitor", text: "Follow each message as it is sent." },
];

const identity = ref("marketer@example.com");
const error = ref("");
const submitting = ref(false);

async function submit() {
  submitting.value = true;
  error.value = "";
  try {
    currentUser.value = await createSession(identity.value);
  } catch (failure) {
    error.value = errorMessage(failure);
  } finally {
    submitting.value = false;
  }
}
</script>

<template>
  <main class="page">
    <section class="story" aria-labelledby="sign-in-story">
      <span class="brand">Smart Campaign Composer</span>
      <h2 id="sign-in-story" class="headline">From a sentence to a campaign you can stand behind.</h2>
      <ol class="flow">
        <li v-for="step in steps" :key="step.title" class="step" :class="{ 'step--decision': step.decision }">
          <span class="marker"><component :is="step.icon" :size="16" aria-hidden="true" /></span>
          <span class="step-text">
            <strong class="step-title">{{ step.title }}</strong>
            <span class="muted">{{ step.text }}</span>
          </span>
        </li>
      </ol>
    </section>

    <section class="access">
      <form class="panel card" data-testid="sign-in" @submit.prevent="submit">
        <span class="brand brand--compact">Smart Campaign Composer</span>
        <header class="panel-head">
          <h1>Sign in</h1>
          <p class="muted">Your name goes next to everything you do here.</p>
        </header>
        <label class="control">
          <span class="label">Username or email</span>
          <input
            v-model="identity"
            class="field"
            type="text"
            autocomplete="username"
            autocapitalize="none"
            spellcheck="false"
            required
            data-testid="sign-in-identity"
          />
        </label>
        <ValidationNotice v-if="error" tone="error" title="We could not sign you in">{{ error }}</ValidationNotice>
        <button class="button button--primary submit" type="submit" data-testid="sign-in-submit" :disabled="submitting">
          {{ submitting ? "Signing in..." : "Continue" }}
        </button>
        <p class="note muted small">Demo sign-in: there is no password, so this name is not verified.</p>
      </form>
    </section>
  </main>
</template>

<style scoped>
.page {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  min-height: 100vh;
  background: var(--color-canvas);
}

.story {
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: var(--space-8);
  padding: var(--space-8) clamp(32px, 7vw, 96px);
  background: var(--color-surface);
  border-right: 1px solid var(--color-border);
}

.brand {
  font-weight: 600;
}

.brand--compact {
  display: none;
}

.headline {
  max-width: 460px;
  font-size: 32px;
  font-weight: 600;
  line-height: 1.2;
  letter-spacing: -0.01em;
}

.flow {
  display: grid;
  max-width: 460px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.step {
  position: relative;
  display: flex;
  gap: var(--space-3);
  padding-bottom: var(--space-6);
}

.step:last-child {
  padding-bottom: 0;
}

/* The line joins one marker to the next. */
.step:not(:last-child)::before {
  position: absolute;
  top: 32px;
  bottom: 0;
  left: 15px;
  width: 1px;
  background: var(--color-border);
  content: "";
}

.marker {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  color: var(--color-text-muted);
  background: var(--color-surface-subtle);
  border-radius: var(--radius-md);
}

.step--decision .marker {
  color: var(--color-primary);
  background: var(--color-primary-soft);
}

.step-text {
  display: grid;
  gap: 2px;
  padding-top: 1px;
}

.step-title {
  font-weight: 600;
}

.step--decision .step-title {
  color: var(--color-primary);
}

.access {
  display: grid;
  place-items: center;
  padding: var(--space-8) var(--space-4);
}

.panel {
  display: grid;
  gap: var(--space-4);
  width: 100%;
  max-width: 400px;
  padding: var(--space-8);
}

.panel-head {
  display: grid;
  gap: var(--space-1);
}

.control {
  display: grid;
  gap: var(--space-1);
}

.label {
  font-weight: 500;
}

.submit {
  width: 100%;
}

.note {
  padding-top: var(--space-3);
  border-top: 1px solid var(--color-border);
}

@media (max-width: 860px) {
  .page {
    grid-template-columns: minmax(0, 1fr);
    grid-template-rows: auto auto;
  }

  /* On a phone the form comes first, and the story explains it below. */
  .access {
    order: -1;
    padding: var(--space-6) var(--space-4);
  }

  .brand--compact {
    display: block;
  }

  .story {
    gap: var(--space-6);
    padding: var(--space-6) var(--space-4);
    border-top: 1px solid var(--color-border);
    border-right: 0;
  }

  .story > .brand {
    display: none;
  }

  .headline {
    font-size: 20px;
  }
}
</style>

<script setup lang="ts">
import type { EmailDesign } from "../api/campaigns";
import { customTemplateName, EMAIL_TEMPLATES } from "../lib/emailTemplates";

defineProps<{ modelValue: string; disabled?: boolean; designs?: EmailDesign[] }>();
// The picture follows how the template's headline stands out.
const headlineStyle = (design: EmailDesign) => design.blocks.find((block) => block.slot === "headline")?.style || "plain";
const emit = defineEmits<{ "update:modelValue": [value: string] }>();
</script>

<template>
  <fieldset class="picker" :disabled="disabled" data-testid="email-templates">
    <legend class="legend">Template</legend>
    <div class="options">
      <label v-for="template in EMAIL_TEMPLATES" :key="template.id" class="option" :class="{ 'option--on': modelValue === template.id }">
        <input
          class="visually-hidden"
          type="radio"
          name="email-template"
          :value="template.id"
          :checked="modelValue === template.id"
          :data-testid="`email-template-${template.id}`"
          @change="emit('update:modelValue', template.id)"
        />
        <span class="sketch" :class="`sketch--${template.id}`" aria-hidden="true">
          <span class="sketch-head"></span>
          <span class="sketch-line"></span>
          <span class="sketch-line sketch-line--short"></span>
          <span class="sketch-button"></span>
        </span>
        <span class="name small">{{ template.name }}</span>
        <span class="hint small muted">{{ template.hint }}</span>
      </label>
      <label v-for="design in designs ?? []" :key="design.id" class="option" :class="{ 'option--on': modelValue === customTemplateName(design.id) }">
        <input
          class="visually-hidden"
          type="radio"
          name="email-template"
          :value="customTemplateName(design.id)"
          :checked="modelValue === customTemplateName(design.id)"
          :data-testid="`email-template-custom-${design.id}`"
          @change="emit('update:modelValue', customTemplateName(design.id))"
        />
        <span class="sketch" :class="`sketch--custom-${headlineStyle(design)}`" :style="{ background: design.page, '--accent': design.accent }" aria-hidden="true">
          <span class="sketch-head"></span>
          <span class="sketch-line"></span>
          <span class="sketch-line sketch-line--short"></span>
          <span class="sketch-button"></span>
        </span>
        <span class="name small">{{ design.name }}</span>
        <span class="hint small muted">Your template</span>
      </label>
    </div>
  </fieldset>
</template>

<style scoped>
.picker {
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.legend {
  padding: 0;
  margin-bottom: var(--space-2);
  font-weight: 500;
}

.options {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
  gap: var(--space-2);
}

.option {
  display: grid;
  align-content: start;
  gap: 2px;
  min-width: 0;
  padding: var(--space-2);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  cursor: pointer;
}

.option:has(input:focus-visible) {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.option--on {
  border-color: var(--color-primary);
  box-shadow: 0 0 0 1px var(--color-primary);
}

.picker:disabled .option {
  cursor: not-allowed;
  opacity: 0.6;
}

.name {
  font-weight: 500;
}

.sketch {
  display: grid;
  gap: 4px;
  align-content: start;
  height: 72px;
  margin-bottom: var(--space-1);
  padding: 8px;
  overflow: hidden;
  background: var(--color-surface-subtle);
  border-radius: var(--radius-sm);
}

.sketch-head {
  height: 10px;
  width: 70%;
  background: #172033;
  border-radius: 2px;
}

.sketch-line {
  height: 4px;
  background: #b8c0cc;
  border-radius: 2px;
}

.sketch-line--short {
  width: 70%;
}

.sketch-button {
  width: 40%;
  height: 10px;
  margin-top: 2px;
  background: #315be8;
  border-radius: 2px;
}

[class*="sketch--custom"] .sketch-line {
  width: 100%;
}

[class*="sketch--custom"] .sketch-line--short {
  width: 70%;
}

[class*="sketch--custom"] .sketch-button {
  background: var(--accent);
}

.sketch--custom-band {
  padding-top: 0;
}

.sketch--custom-band .sketch-head {
  width: calc(100% + 16px);
  height: 24px;
  margin: 0 -8px 2px;
  background: var(--accent);
  border-radius: 0;
}

.sketch--custom-display .sketch-head {
  height: 20px;
  background: var(--accent);
}

.sketch--custom-rule .sketch-head {
  width: 40%;
  height: 4px;
  margin-bottom: 6px;
  background: var(--accent);
}

.sketch--custom-box .sketch-head {
  height: 18px;
  background: var(--accent);
}

.sketch--banner {
  padding-top: 0;
}

.sketch--banner .sketch-head {
  width: calc(100% + 16px);
  height: 24px;
  margin: 0 -8px 2px;
  background: #172f86;
  border-radius: 0;
}

.sketch--banner .sketch-button {
  background: #172f86;
  border-radius: 999px;
}

.sketch--offer {
  justify-items: center;
  border-top: 3px solid #c2410c;
}

.sketch--offer .sketch-head {
  width: 100%;
  height: 18px;
  background: #fff4ed;
  border: 1px dashed #c2410c;
}

.sketch--offer .sketch-line {
  width: 90%;
}

.sketch--offer .sketch-line--short {
  width: 60%;
}

.sketch--offer .sketch-button {
  background: #c2410c;
}
</style>

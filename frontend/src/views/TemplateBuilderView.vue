<script setup lang="ts">
import { ArrowDown, ArrowLeft, ArrowUp, Copy, GripVertical, Heading, Image as ImageIcon, MousePointerClick, Minus, Sparkles, Stamp, Trash2, Type } from "lucide-vue-next";
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";

import { errorMessage, getBrand, getEmailDesigns, previewEmail, saveEmailDesign, type BlockType, type Brand, type EmailBlock, type EmailDesign } from "../api/campaigns";
import AssetLibrary from "../components/AssetLibrary.vue";
import ValidationNotice from "../components/ValidationNotice.vue";
import { BLOCK_TYPES, blockLabel, brandedDesign, LAYOUTS, moved, newBlock, SAMPLE_EMAIL } from "../lib/emailTemplates";
import { navigate } from "../lib/navigation";

const props = defineProps<{ designId?: string }>();

const PREVIEW_DELAY_MS = 200;
const MAX_BLOCKS = 20;
const HEX = /^#[0-9A-Fa-f]{6}$/;
const ICONS = { logo: Stamp, image: ImageIcon, heading: Heading, text: Type, button: MousePointerClick, divider: Minus };
const ALIGNS = ["left", "center", "right"];

const design = ref<EmailDesign>(brandedDesign());
const brand = ref<Brand | null>(null);
const selectedId = ref("");
const side = ref<"blocks" | "settings">("blocks");
const settingsPanel = ref<HTMLElement | null>(null);
const choosingPicture = ref(false);
const device = ref<"desktop" | "phone">("desktop");
const html = ref("");
const frame = ref<HTMLIFrameElement | null>(null);
const loading = ref(true);
const saving = ref(false);
const dirty = ref(false);
const error = ref("");
const previewError = ref(false);
const dragging = ref<{ from: number } | { add: BlockType } | null>(null);
const dropAt = ref(-1);

let previewTimer: ReturnType<typeof setTimeout> | undefined;
let latestPreview = 0;

const selected = computed(() => design.value.blocks.find((block) => block.id === selectedId.value) ?? null);
const selectedIndex = computed(() => design.value.blocks.findIndex((block) => block.id === selectedId.value));
const colorsValid = computed(() => HEX.test(design.value.accent) && HEX.test(design.value.page));
const full = computed(() => design.value.blocks.length >= MAX_BLOCKS);
const canSave = computed(() => design.value.name.trim() !== "" && colorsValid.value && !saving.value);
const pictureUrl = (block: EmailBlock) => (block.asset_id ? `/api/assets/${block.asset_id}/file` : "");

function select(id: string) {
  selectedId.value = id;
  choosingPicture.value = false;
}

// Whatever was clicked, its settings must end up where they can be seen. Beside the preview
// the panel stays in view by itself; moving the page there would pull the block from under
// the pointer. Only in one column, where the panel is above the preview, the page has to move.
async function openBlock(id: string) {
  select(id);
  side.value = "settings";
  if (!window.matchMedia?.("(max-width: 760px)").matches) return;
  await nextTick();
  settingsPanel.value?.scrollIntoView?.({ block: "nearest" });
}

// An empty picture block says "Choose a picture" in the preview, so clicking it does that.
function openFromPreview(id: string) {
  void openBlock(id);
  const block = selected.value;
  if (block && (block.type === "image" || block.type === "logo") && !block.asset_id) choosingPicture.value = true;
}

function addBlock(type: BlockType, at = -1) {
  if (full.value) return;
  const block = newBlock(type, brand.value);
  const blocks = [...design.value.blocks];
  const place = at >= 0 ? at : selectedIndex.value >= 0 ? selectedIndex.value + 1 : blocks.length;
  blocks.splice(place, 0, block);
  design.value.blocks = blocks;
  select(block.id);
}

function move(from: number, to: number) {
  if (to < 0 || to >= design.value.blocks.length || from === to) return;
  design.value.blocks = moved(design.value.blocks, from, to);
}

function duplicate(block: EmailBlock) {
  if (full.value || block.slot) return;
  const copy = { ...newBlock(block.type), ...block, id: newBlock(block.type).id };
  const blocks = [...design.value.blocks];
  blocks.splice(selectedIndex.value + 1, 0, copy);
  design.value.blocks = blocks;
  select(copy.id);
}

function remove(block: EmailBlock) {
  if (block.slot) return;
  design.value.blocks = design.value.blocks.filter((item) => item.id !== block.id);
  select("");
}

function useLayout(id: string) {
  const layout = LAYOUTS.find((item) => item.id === id);
  if (!layout) return;
  design.value.blocks = layout.blocks(brand.value);
  select("");
}

function applyBrand() {
  if (!brand.value) return;
  const { accent, page, button, font, footer } = brand.value;
  Object.assign(design.value, { accent, page, button, font, footer });
}

// Native drag and drop, for both reordering and bringing in a new block. The arrow buttons
// do the same by keyboard and on touch screens.
function dropInto(index: number) {
  const from = dragging.value;
  dragging.value = null;
  dropAt.value = -1;
  if (!from) return;
  if ("add" in from) {
    addBlock(from.add, index);
    return;
  }
  move(from.from, index > from.from ? index - 1 : index);
}

async function refreshPreview() {
  if (!colorsValid.value) return;
  const request = ++latestPreview;
  try {
    // The backend draws it, so this is what people would actually receive.
    const email = await previewEmail({ ...SAMPLE_EMAIL, email_template: "custom:preview", email_design: { ...design.value, name: design.value.name.trim() || "Preview" } }, "Ayşe", "", true);
    if (request !== latestPreview) return;
    draw(email.html);
    previewError.value = false;
  } catch {
    if (request === latestPreview) previewError.value = true;
  }
}

// Same-origin lets this page read clicks on the blocks. Safari also needs allow-scripts, or it
// drops this page's listeners on the frame without a word. Nothing inside the email can run:
// the editing render carries its own script-src 'none'.
function frameDocument() {
  return frame.value?.contentDocument ?? null;
}

// Changing the words in place keeps the scroll position and does not flash on each key.
let frameReady = false;
function draw(next: string) {
  const doc = frameDocument();
  if (!frameReady || !doc) {
    html.value = next;
    return;
  }
  doc.documentElement.innerHTML = new DOMParser().parseFromString(next, "text/html").documentElement.innerHTML;
  markSelected();
}

function markSelected() {
  const doc = frameDocument();
  if (!doc?.head) return;
  let style = doc.getElementById("builder-marks");
  if (!style) {
    style = doc.createElement("style");
    style.id = "builder-marks";
    doc.head.append(style);
  }
  const chosen = selectedId.value ? `[data-block="${CSS.escape(selectedId.value)}"]{outline:2px solid #315BE8;outline-offset:-2px}` : "";
  style.textContent = `[data-block]{cursor:pointer}[data-block]:hover{outline:1px dashed #315BE8;outline-offset:-1px}${chosen}`;
}

function frameLoaded() {
  const doc = frameDocument();
  if (!doc) return;
  frameReady = true;
  markSelected();
  doc.addEventListener("click", (event) => {
    event.preventDefault();
    const block = (event.target as Element).closest?.("[data-block]");
    const id = block?.getAttribute("data-block");
    if (id) openFromPreview(id);
    else select("");
  }, true);
}

async function save() {
  saving.value = true;
  error.value = "";
  try {
    await saveEmailDesign(props.designId ?? "", design.value);
    dirty.value = false;
    navigate("/templates");
  } catch (failure) {
    error.value = errorMessage(failure);
  } finally {
    saving.value = false;
  }
}

function warnBeforeLeaving(event: BeforeUnloadEvent) {
  if (dirty.value) event.preventDefault();
}

watch(design, () => {
  dirty.value = !loading.value;
  clearTimeout(previewTimer);
  previewTimer = setTimeout(refreshPreview, PREVIEW_DELAY_MS);
}, { deep: true });
watch(selectedId, markSelected);

onMounted(async () => {
  window.addEventListener("beforeunload", warnBeforeLeaving);
  try {
    brand.value = await getBrand();
    if (props.designId) {
      const found = (await getEmailDesigns()).find((item) => item.id === props.designId);
      if (found) {
        const { version: _version, campaigns: _campaigns, updated_at: _updated, ...stored } = found;
        design.value = stored;
      } else {
        error.value = "This template does not exist any more.";
      }
    } else {
      design.value = brandedDesign(brand.value);
    }
  } catch (failure) {
    error.value = errorMessage(failure);
  }
  await refreshPreview();
  await nextTick();
  loading.value = false;
});
onBeforeUnmount(() => {
  clearTimeout(previewTimer);
  window.removeEventListener("beforeunload", warnBeforeLeaving);
});
</script>

<template>
  <div class="studio">
    <header class="top">
      <div class="title-field">
        <label for="design-name">Template name</label>
        <input id="design-name" v-model="design.name" class="field" maxlength="60" placeholder="For example: Winter campaigns" data-testid="design-name" />
      </div>
      <div class="top-actions">
        <span v-if="dirty" class="small muted" data-testid="design-dirty">Unsaved changes</span>
        <a class="small" href="/templates">Cancel</a>
        <button class="button button--primary" type="button" :disabled="!canSave" data-testid="design-save" @click="save">{{ saving ? "Saving..." : "Save template" }}</button>
      </div>
    </header>

    <ValidationNotice v-if="error" tone="error" title="The template was not saved">{{ error }}</ValidationNotice>
    <p v-if="loading" class="muted" role="status">Loading the editor...</p>

    <div v-show="!loading" class="workspace">
      <div class="side">
        <div class="devices side-tabs" role="group" aria-label="Side panel">
          <button class="device" :class="{ 'device--on': side === 'blocks' }" type="button" :aria-pressed="side === 'blocks'" data-testid="side-blocks" @click="side = 'blocks'">Blocks</button>
          <button class="device" :class="{ 'device--on': side === 'settings' }" type="button" :aria-pressed="side === 'settings'" data-testid="side-settings" @click="side = 'settings'">Settings</button>
        </div>
        <aside class="panel card" :class="{ 'panel--off': side === 'settings' }" aria-label="Blocks">
          <section class="group">
            <h2>Add a block</h2>
            <div class="palette">
              <button
                v-for="item in BLOCK_TYPES"
                :key="item.type"
                class="tile small"
                type="button"
                draggable="true"
                :disabled="full"
                :data-testid="`add-${item.type}`"
                @click="addBlock(item.type)"
                @dragstart="dragging = { add: item.type }"
                @dragend="dragging = null; dropAt = -1"
              >
                <component :is="ICONS[item.type]" :size="16" aria-hidden="true" />
                {{ item.label }}
              </button>
            </div>
            <p v-if="full" class="small muted">A template can have up to {{ MAX_BLOCKS }} blocks.</p>
          </section>

          <section class="group">
            <h2>In this email</h2>
            <ol class="layers" data-testid="layers" @dragover.prevent @drop.prevent="dropInto(design.blocks.length)">
              <li
                v-for="(block, index) in design.blocks"
                :key="block.id"
                class="layer"
                :class="{ 'layer--on': block.id === selectedId, 'layer--drop': dropAt === index }"
                draggable="true"
                :data-testid="`layer-${block.slot ?? block.type}`"
                @dragstart="dragging = { from: index }"
                @dragend="dragging = null; dropAt = -1"
                @dragover.prevent.stop="dropAt = index"
                @drop.prevent.stop="dropInto(index)"
              >
                <GripVertical class="grip" :size="14" aria-hidden="true" />
                <button class="layer-name small" type="button" :aria-pressed="block.id === selectedId" @click="openBlock(block.id)">
                  <component :is="ICONS[block.type]" :size="14" aria-hidden="true" />
                  <span>{{ blockLabel(block) }}</span>
                  <Sparkles v-if="block.slot" class="ai" :size="12" aria-label="Written by AI" />
                </button>
                <button class="icon" type="button" :disabled="index === 0" :aria-label="`Move ${blockLabel(block)} up`" data-testid="layer-up" @click="move(index, index - 1)"><ArrowUp :size="13" aria-hidden="true" /></button>
                <button class="icon" type="button" :disabled="index === design.blocks.length - 1" :aria-label="`Move ${blockLabel(block)} down`" data-testid="layer-down" @click="move(index, index + 1)"><ArrowDown :size="13" aria-hidden="true" /></button>
              </li>
            </ol>
            <p class="small muted legend"><Sparkles :size="12" aria-hidden="true" /> The AI writes these for each campaign. Move them anywhere; they cannot be removed.</p>
          </section>
        </aside>
        <aside ref="settingsPanel" class="panel panel--settings card" :class="{ 'panel--off': side === 'blocks' }" aria-label="Properties" data-testid="properties">
          <template v-if="selected">
            <div class="props-head">
              <h2>{{ blockLabel(selected) }}</h2>
              <button class="back small" type="button" data-testid="show-style" @click="select('')"><ArrowLeft :size="14" aria-hidden="true" /> Email style</button>
            </div>

            <p v-if="selected.slot" class="small note" data-testid="slot-note">
              <Sparkles :size="12" aria-hidden="true" /> The AI writes this for each campaign, and you check it before approval. Here you choose how it looks.
            </p>

            <template v-if="selected.type === 'logo' || selected.type === 'image'">
              <div class="field-group">
                <span class="label">Picture</span>
                <div class="picture">
                  <img v-if="pictureUrl(selected)" :src="pictureUrl(selected)" alt="" data-testid="block-picture" />
                  <span v-else class="small muted">None yet</span>
                </div>
                <button class="button button--secondary" type="button" data-testid="block-choose-picture" @click="choosingPicture = !choosingPicture">{{ choosingPicture ? "Close" : pictureUrl(selected) ? "Change picture" : "Choose picture" }}</button>
                <AssetLibrary v-if="choosingPicture" :selected="selected.asset_id" @choose="(asset) => { selected!.asset_id = asset.id; choosingPicture = false; }" />
              </div>
              <div class="field-group">
                <label for="block-width">Width: {{ selected.width }}%</label>
                <input id="block-width" v-model.number="selected.width" type="range" min="10" max="100" step="5" data-testid="block-width" />
                <span v-if="selected.type === 'image' && selected.width === 100" class="small muted">At 100% it reaches both edges of the email.</span>
              </div>
              <div class="field-group">
                <label for="block-alt">Description</label>
                <input id="block-alt" v-model="selected.alt" class="field" maxlength="125" placeholder="What the picture shows" data-testid="block-alt" />
                <span class="small muted">For people whose email hides pictures.</span>
              </div>
              <div class="field-group">
                <label for="block-link">Link <span class="muted small">Optional</span></label>
                <input id="block-link" v-model="selected.link" class="field" type="url" placeholder="https://" data-testid="block-link" />
              </div>
            </template>

            <div v-if="selected.type === 'heading' && !selected.slot" class="field-group">
              <label for="block-text">Words</label>
              <input id="block-text" v-model="selected.text" class="field" maxlength="200" data-testid="block-text" />
            </div>
            <div v-if="selected.type === 'text' && !selected.slot" class="field-group">
              <label for="block-text">Words</label>
              <textarea id="block-text" v-model="selected.text" class="field" rows="5" maxlength="1000" data-testid="block-text"></textarea>
            </div>
            <template v-if="selected.type === 'button' && !selected.slot">
              <div class="field-group">
                <label for="block-label">Label</label>
                <input id="block-label" v-model="selected.label" class="field" maxlength="40" data-testid="block-label" />
              </div>
              <div class="field-group">
                <label for="block-url">Link</label>
                <input id="block-url" v-model="selected.url" class="field" type="url" placeholder="https://" data-testid="block-url" />
              </div>
            </template>
            <p v-if="!selected.slot && ['heading', 'text', 'button'].includes(selected.type)" class="small muted" data-testid="fixed-note">
              These words stay the same in every campaign and are not translated. {{ "{" + "{first_name}" + "}" }} fills in the person's name.
            </p>

            <div v-if="selected.type === 'heading'" class="field-group">
              <label for="block-style">Stands out as</label>
              <select id="block-style" v-model="selected.style" class="field" data-testid="block-style">
                <option value="plain">Plain</option>
                <option value="band">Color band</option>
                <option value="box">Box</option>
              </select>
            </div>

            <fieldset v-if="selected.type !== 'divider'" class="field-group">
              <legend>Line up</legend>
              <div class="choices">
                <label v-for="option in ALIGNS" :key="option" class="choice small" :class="{ 'choice--on': (selected.align || 'left') === option }">
                  <input v-model="selected.align" class="visually-hidden" type="radio" name="block-align" :value="option" :data-testid="`block-align-${option}`" />
                  {{ option[0].toUpperCase() + option.slice(1) }}
                </label>
              </div>
            </fieldset>

            <div v-if="!selected.slot" class="block-actions">
              <button class="plain small with-icon" type="button" :disabled="full" data-testid="block-duplicate" @click="duplicate(selected)"><Copy :size="13" aria-hidden="true" /> Duplicate</button>
              <button class="plain small with-icon" type="button" data-testid="block-remove" @click="remove(selected)"><Trash2 :size="13" aria-hidden="true" /> Remove</button>
            </div>
          </template>

          <template v-else>
            <h2>Email style</h2>
            <div v-if="!designId" class="field-group">
              <span class="label">Start from a layout</span>
              <div class="choices">
                <button v-for="layout in LAYOUTS" :key="layout.id" class="choice small" type="button" :title="layout.hint" :data-testid="`layout-${layout.id}`" @click="useLayout(layout.id)">{{ layout.name }}</button>
              </div>
              <span class="small muted">This replaces the blocks you have now.</span>
            </div>
            <div class="field-group">
              <label for="design-accent">Main color</label>
              <div class="color">
                <input :value="HEX.test(design.accent) ? design.accent : '#000000'" class="picker" type="color" aria-label="Pick the main color" @input="design.accent = ($event.target as HTMLInputElement).value.toUpperCase()" />
                <input id="design-accent" v-model="design.accent" class="field mono" maxlength="7" data-testid="design-accent" />
              </div>
            </div>
            <div class="field-group">
              <label for="design-page">Background</label>
              <div class="color">
                <input :value="HEX.test(design.page) ? design.page : '#000000'" class="picker" type="color" aria-label="Pick the background color" @input="design.page = ($event.target as HTMLInputElement).value.toUpperCase()" />
                <input id="design-page" v-model="design.page" class="field mono" maxlength="7" data-testid="design-page" />
              </div>
            </div>
            <p v-if="!colorsValid" class="small problem" role="alert" data-testid="design-color-problem">Colors must look like #315BE8.</p>
            <p v-else class="small muted">Text on the main color turns dark or white by itself, so it stays readable.</p>
            <div class="field-group">
              <label for="design-button">Button shape</label>
              <select id="design-button" v-model="design.button" class="field" data-testid="design-button">
                <option value="square">Square</option>
                <option value="rounded">Rounded</option>
                <option value="pill">Pill</option>
              </select>
            </div>
            <div class="field-group">
              <label for="design-corners">Corners</label>
              <select id="design-corners" v-model.number="design.corners" class="field" data-testid="design-corners">
                <option :value="0">None</option>
                <option :value="8">Small</option>
                <option :value="16">Large</option>
              </select>
            </div>
            <div class="field-group">
              <label for="design-font">Font</label>
              <select id="design-font" v-model="design.font" class="field" data-testid="design-font">
                <option value="sans">Clean (sans serif)</option>
                <option value="serif">Classic (serif)</option>
              </select>
            </div>
            <div class="field-group">
              <label for="design-footer">Footer details <span class="muted small">Optional</span></label>
              <input id="design-footer" v-model="design.footer" class="field" maxlength="200" placeholder="Company name, address" data-testid="design-footer" />
              <span class="small muted">The unsubscribe link is always added below this.</span>
            </div>
            <button class="plain small" type="button" data-testid="apply-brand" @click="applyBrand">Use my brand kit's colors and details</button>
            <p v-if="designId" class="small muted">Campaigns already using this template keep the look they started with.</p>
          </template>
        </aside>
      </div>

      <section class="stage" aria-label="Preview">
        <div class="stage-head">
          <h2>Preview <span class="small muted">Click a block to change it</span></h2>
          <div class="devices" role="group" aria-label="Preview size">
            <button class="device" :class="{ 'device--on': device === 'desktop' }" type="button" :aria-pressed="device === 'desktop'" data-testid="device-desktop" @click="device = 'desktop'">Computer</button>
            <button class="device" :class="{ 'device--on': device === 'phone' }" type="button" :aria-pressed="device === 'phone'" data-testid="device-phone" @click="device = 'phone'">Phone</button>
          </div>
        </div>
        <p v-if="previewError" class="small problem" role="alert">The preview could not be drawn.</p>
        <div class="screen card" :class="`screen--${device}`">
          <iframe v-if="html" ref="frame" class="frame" title="Template preview" sandbox="allow-same-origin allow-scripts" :srcdoc="html" data-testid="design-frame" @load="frameLoaded"></iframe>
        </div>
        <p class="small muted">Sample words. Each campaign brings its own, in its audience's language.</p>
      </section>
    </div>
  </div>
</template>

<style scoped>
.studio,
.group,
.stage,
.field-group,
.title-field {
  display: grid;
  gap: var(--space-2);
  min-width: 0;
}

.studio {
  gap: var(--space-4);
}

.top {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  justify-content: space-between;
  gap: var(--space-3);
}

.title-field {
  flex: 1 1 280px;
  max-width: 420px;
}

.top-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
}

h2,
p {
  margin: 0;
}

h2 {
  font-size: inherit;
  font-weight: 600;
}

label,
legend,
.label {
  padding: 0;
  font-weight: 500;
}

fieldset {
  margin: 0;
  padding: 0;
  border: 0;
}

.workspace {
  display: grid;
  grid-template-columns: 220px minmax(0, 1fr) 260px;
  align-items: start;
  gap: var(--space-4);
}

.side {
  display: contents;
}

.side > .side-tabs {
  display: none;
}

.panel {
  position: sticky;
  top: var(--space-4);
  display: grid;
  gap: var(--space-4);
  min-width: 0;
  max-height: calc(100vh - 2 * var(--space-4));
  overflow-y: auto;
  padding: var(--space-4);
}

.panel--settings {
  order: 2;
}

.palette {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-2);
}

.tile {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 40px;
  padding: 0 var(--space-2);
  color: var(--color-text);
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  font: inherit;
  cursor: grab;
}

.tile:hover:not(:disabled) {
  background: var(--color-surface-subtle);
}

.tile:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.layers {
  display: grid;
  gap: 2px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.layer {
  display: flex;
  align-items: center;
  gap: 2px;
  min-height: 36px;
  padding: 0 2px;
  border-top: 2px solid transparent;
  border-radius: var(--radius-sm);
}

.layer:hover {
  background: var(--color-surface-subtle);
}

.layer--on {
  background: var(--color-primary-soft);
}

.layer--drop {
  border-top-color: var(--color-primary);
}

.grip {
  flex: none;
  color: var(--color-text-muted);
  cursor: grab;
}

.layer-name {
  display: flex;
  flex: 1;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
  min-height: 32px;
  padding: 0 var(--space-1);
  color: var(--color-text);
  background: none;
  border: 0;
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.ai {
  flex: none;
  color: var(--color-primary);
}

.icon {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  color: var(--color-text-muted);
  background: none;
  border: 0;
  border-radius: var(--radius-sm);
  cursor: pointer;
}

.icon:hover:not(:disabled) {
  color: var(--color-text);
  background: var(--color-surface);
}

.icon:disabled {
  cursor: default;
  opacity: 0.3;
}

.legend,
.note,
.with-icon {
  display: flex;
  align-items: flex-start;
  gap: var(--space-1);
}

.legend svg,
.note svg {
  flex: none;
  margin-top: 3px;
}

.note {
  padding: var(--space-2);
  background: var(--color-primary-soft);
  border-radius: var(--radius-sm);
}

.stage {
  order: 1;
  gap: var(--space-3);
}

.stage-head,
.props-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.back {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  min-height: 32px;
  padding: 0 var(--space-2);
  color: var(--color-text);
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  font: inherit;
  cursor: pointer;
}

.back:hover {
  background: var(--color-surface-subtle);
}

.stage-head .small {
  margin-left: var(--space-2);
  font-weight: 400;
}

.devices {
  display: flex;
  gap: var(--space-1);
}

.device {
  min-height: 32px;
  padding: 0 var(--space-3);
  color: var(--color-text-muted);
  background: transparent;
  border: 0;
  border-radius: var(--radius-sm);
  font: inherit;
  cursor: pointer;
}

.device--on {
  color: var(--color-primary);
  background: var(--color-primary-soft);
  font-weight: 500;
}

.screen {
  display: flex;
  overflow-x: auto;
  background: var(--color-surface-subtle);
}

/* Auto margins center it, and unlike centering the row they never cut off its left side. */
.frame {
  display: block;
  width: 100%;
  height: 720px;
  margin-inline: auto;
  border: 0;
  transition: width var(--transition-standard);
}

/* Wide enough that the email's own phone layout stays off. */
.screen--desktop .frame {
  flex: none;
  min-width: 640px;
}

.screen--phone .frame {
  width: 375px;
  max-width: 100%;
}

.problem {
  color: var(--color-error);
}

.picture {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 96px;
  padding: var(--space-2);
  background: var(--color-surface-subtle);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
}

.picture img {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
}

input[type="range"] {
  width: 100%;
  accent-color: var(--color-primary);
}

.choices {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.choice {
  display: inline-flex;
  align-items: center;
  min-height: 34px;
  padding: 0 var(--space-3);
  color: var(--color-text);
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  font: inherit;
  font-weight: 400;
  cursor: pointer;
}

.choice:hover {
  background: var(--color-surface-subtle);
}

.choice:has(input:focus-visible) {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}

.choice--on {
  border-color: var(--color-primary);
  box-shadow: 0 0 0 1px var(--color-primary);
}

.block-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: var(--space-3);
  padding-top: var(--space-2);
  border-top: 1px solid var(--color-border);
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

.plain:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.with-icon {
  align-items: center;
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

.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
}

/* No room for three columns: the list and the settings take turns beside the preview. */
@media (max-width: 1180px) {
  .workspace {
    grid-template-columns: minmax(220px, 260px) minmax(0, 1fr);
  }

  .side {
    position: sticky;
    top: var(--space-4);
    display: grid;
    gap: var(--space-2);
    min-width: 0;
    max-height: calc(100vh - 2 * var(--space-4));
    overflow-y: auto;
  }

  .side > .side-tabs {
    display: flex;
    justify-self: start;
  }

  .panel {
    position: static;
    max-height: none;
    overflow-y: visible;
  }

  .panel--off {
    display: none;
  }
}

@media (max-width: 760px) {
  .workspace {
    grid-template-columns: minmax(0, 1fr);
  }

  .side {
    position: static;
    max-height: none;
    overflow-y: visible;
  }
}

@media (prefers-reduced-motion: reduce) {
  .frame {
    transition: none;
  }
}
</style>

import { describe, expect, it } from "vitest";

import tokens from "./tokens.css?raw";

function token(name: string): string {
  const match = tokens.match(new RegExp(`--${name}:\\s*(#[0-9A-Fa-f]{6})`));
  if (!match) throw new Error(`token --${name} is missing`);
  return match[1];
}

function luminance(hex: string): number {
  const [r, g, b] = [1, 3, 5]
    .map((i) => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrast(a: string, b: string): number {
  const [light, dark] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (light + 0.05) / (dark + 0.05);
}

const AA_TEXT = 4.5;
const AA_NON_TEXT = 3;

describe("Signal color contrast", () => {
  it.each([
    ["color-text", "color-surface"],
    ["color-text", "color-canvas"],
    ["color-text", "color-surface-subtle"],
    ["color-text-muted", "color-surface"],
    ["color-text-muted", "color-canvas"],
    ["color-text-muted", "color-surface-subtle"],
    ["color-text-muted", "color-primary-soft"],
    ["color-primary", "color-surface"],
    ["color-primary", "color-primary-soft"],
    ["color-ai", "color-surface"],
    ["color-success", "color-surface"],
    ["color-success", "color-canvas"],
    ["color-warning", "color-surface"],
    ["color-warning", "color-canvas"],
    ["color-error", "color-surface"],
    ["color-error", "color-canvas"],
    ["color-on-primary", "color-primary"],
  ])("%s on %s meets AA for text", (foreground, background) => {
    expect(contrast(token(foreground), token(background))).toBeGreaterThanOrEqual(AA_TEXT);
  });

  it("input borders are visible against the surface", () => {
    expect(contrast(token("color-border-input"), token("color-surface"))).toBeGreaterThanOrEqual(AA_NON_TEXT);
  });
});

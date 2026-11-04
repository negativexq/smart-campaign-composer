// Choices kept in this browser. Without storage each one starts at its default.
function read(key: string, allowed: { value: string }[], fallback: string): string {
  try {
    const stored = window.localStorage.getItem(key);
    if (allowed.some((option) => option.value === stored)) return stored as string;
  } catch {
    // Storage can be blocked.
  }
  return fallback;
}

function write(key: string, value: string) {
  try {
    window.localStorage.setItem(key, value);
  } catch {
    // Storage can be blocked. The choice still holds on the open page.
  }
}

export const AUTO_REFRESH = [
  { value: "0", label: "Off" },
  { value: "5", label: "5 sec" },
  { value: "10", label: "10 sec" },
  { value: "30", label: "30 sec" },
  { value: "60", label: "1 min" },
];
export const storedAutoRefresh = () => read("delivery-monitor-refresh", AUTO_REFRESH, "10");
export const storeAutoRefresh = (value: string) => write("delivery-monitor-refresh", value);

const CHANNEL_SETS = [{ value: "sms,email" }, { value: "sms" }, { value: "email" }];
export const defaultChannels = () => read("new-campaign-channels", CHANNEL_SETS, "sms,email").split(",");
export const storeDefaultChannels = (channels: string[]) => write("new-campaign-channels", ["sms", "email"].filter((channel) => channels.includes(channel)).join(","));

export const THEMES = [
  { value: "light", label: "Light" },
  { value: "dark", label: "Dark" },
  { value: "system", label: "Same as this device" },
];
const darkDevice = () => (typeof window.matchMedia === "function" ? window.matchMedia("(prefers-color-scheme: dark)") : null);
export const storedTheme = () => read("theme", THEMES, "light");

export function applyTheme(theme = storedTheme()) {
  const dark = theme === "dark" || (theme === "system" && Boolean(darkDevice()?.matches));
  document.documentElement.dataset.theme = dark ? "dark" : "light";
}

export function storeTheme(value: string) {
  write("theme", value);
  applyTheme(value);
}

/** Sets the theme before the first paint, and follows the device while "system" is chosen. */
export function startTheme() {
  applyTheme();
  darkDevice()?.addEventListener("change", () => {
    if (storedTheme() === "system") applyTheme("system");
  });
}

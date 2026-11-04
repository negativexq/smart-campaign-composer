export function formatDateTime(value: string): string {
  return new Date(value).toLocaleString("en-GB", { dateStyle: "medium", timeStyle: "short" });
}

export function formatChannel(channel: string): string {
  return channel === "sms" ? "SMS" : "Email";
}

/** How much of a list is on screen, in words. */
export function shownOf(shown: number, total: number, one: string, many: string): string {
  const count = (value: number) => value.toLocaleString("en-GB");
  if (shown < total) return `Showing ${count(shown)} of ${count(total)} ${many}`;
  return `${count(total)} ${total === 1 ? one : many}`;
}

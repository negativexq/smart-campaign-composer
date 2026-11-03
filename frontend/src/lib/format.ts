export function formatDateTime(value: string): string {
  return new Date(value).toLocaleString("en-GB", { dateStyle: "medium", timeStyle: "short" });
}

export function formatChannel(channel: string): string {
  return channel === "sms" ? "SMS" : "Email";
}

import type { Progress, TimelineBucket } from "../api/campaigns";
import { formatChannel } from "./format";
import { deliveryStatusMeta, type DeliveryStatus } from "./deliveryStatus";

export const REASONS: Record<string, string> = {
  no_consent: "No consent for this channel",
  consent_withdrawn: "Consent withdrawn before sending",
  server_error: "Provider error",
  rejected: "Rejected by the provider",
  timeout: "No response from the provider",
  rate_limited: "Rate limited by the provider",
  rate_limiter_unavailable: "Waiting for the rate limiter",
  provider_unreachable: "Provider cannot be reached",
  lease_expired: "A worker stopped while sending",
};

export const OUTCOMES: Record<string, string> = {
  accepted: "Accepted",
  rate_limited: "Rate limited",
  server_error: "Provider error",
  rejected: "Rejected",
  timeout: "No response",
};

/** What the deliveries table is narrowed to. An empty value means no filter. */
export type DeliveryQuery = {
  status: string;
  channel: string;
  reason: string;
  outcome: string;
  from: string;
  to: string;
  calls: string;
  city: string;
  opened: "" | "yes" | "no";
  arrival: "" | "delivered" | "bounced" | "waiting";
  variant: "" | "A" | "B";
};

export const CALLS: Record<string, string> = {
  "0": "Never called",
  "1": "Called once",
  "2": "Called twice",
  "2+": "Called more than once",
  "3+": "Called three times or more",
};

export const noFilter = (): DeliveryQuery => ({ status: "", channel: "", reason: "", outcome: "", from: "", to: "", calls: "", city: "", opened: "", arrival: "", variant: "" });

export const ARRIVALS: Record<DeliveryQuery["arrival"] & string, string> = {
  "": "",
  delivered: "Delivered",
  bounced: "Bounced",
  waiting: "Waiting for a report",
};

/** What the provider says became of an accepted message, if anything yet. */
export function arrivalOf(row: { status: string; delivered_at?: string | null; bounced_at?: string | null }): { label: string; at: string | null } | null {
  if (row.bounced_at) return { label: ARRIVALS.bounced, at: row.bounced_at };
  if (row.delivered_at) return { label: ARRIVALS.delivered, at: row.delivered_at };
  return row.status === "SENT" ? { label: ARRIVALS.waiting, at: null } : null;
}

export function isFiltered(query: DeliveryQuery): boolean {
  return Object.values(query).some(Boolean);
}

/** A selection is the same when all of its fields match, so clicking it again can clear it. */
export function matches(query: DeliveryQuery, selection: Partial<DeliveryQuery>): boolean {
  return (Object.keys(query) as (keyof DeliveryQuery)[]).every((key) => query[key] === { ...noFilter(), ...selection }[key]);
}

export type ChannelSegment = { status: DeliveryStatus | "WAITING"; label: string; count: number };
export type ChannelBar = { id: string; label: string; total: number; segments: ChannelSegment[] };

const FINAL_ORDER: DeliveryStatus[] = ["SENT", "SKIPPED", "FAILED", "UNKNOWN"];

/** Only channels the campaign used appear, with waiting messages as one segment. */
export function channelBars(progress: Progress): ChannelBar[] {
  return ["sms", "email"].flatMap((id) => {
    const counts = progress.by_channel[id] ?? {};
    const waiting = (counts.QUEUED ?? 0) + (counts.SENDING ?? 0) + (counts.RETRYING ?? 0);
    const segments: ChannelSegment[] = FINAL_ORDER.map((status) => ({
      status,
      label: deliveryStatusMeta[status].label,
      count: counts[status] ?? 0,
    }));
    segments.push({ status: "WAITING", label: "Waiting", count: waiting });
    const total = segments.reduce((sum, segment) => sum + segment.count, 0);
    return total ? [{ id, label: formatChannel(id), total, segments: segments.filter((segment) => segment.count) }] : [];
  });
}

/** SMS has reads and email has opens, so the word follows the channel. */
export const openedWord = (channel: string) => (channel === "sms" ? "read" : "opened");

export const notAccepted = (bucket: TimelineBucket) => bucket.rate_limited + bucket.server_error + bucket.rejected + bucket.timeout;
export const callsIn = (bucket: TimelineBucket) => bucket.accepted + notAccepted(bucket);

/** Rounds up to a tick people read easily, so the top of the axis is a clean number. */
export function niceMax(value: number): number {
  if (value <= 5) return Math.max(value, 1);
  const power = 10 ** Math.floor(Math.log10(value));
  const step = [1, 2, 5, 10].find((candidate) => candidate * power >= value) ?? 10;
  return step * power;
}

export function formatBucket(at: string, bucketSeconds: number): string {
  const options: Intl.DateTimeFormatOptions =
    bucketSeconds >= 3600
      ? { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", hourCycle: "h23" }
      : bucketSeconds >= 60
        ? { hour: "2-digit", minute: "2-digit", hourCycle: "h23" }
        : { hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" };
  return new Intl.DateTimeFormat("en-GB", options).format(new Date(at));
}

export function formatBucketWidth(seconds: number): string {
  if (seconds < 60) return seconds === 1 ? "second" : `${seconds} seconds`;
  if (seconds < 3600) return seconds === 60 ? "minute" : `${Math.round(seconds / 60)} minutes`;
  if (seconds < 86400) return seconds === 3600 ? "hour" : `${Math.round(seconds / 3600)} hours`;
  return seconds === 86400 ? "day" : `${Math.round(seconds / 86400)} days`;
}

/** The filter that lists the deliveries called during one bucket. */
export function bucketQuery(bucket: TimelineBucket, bucketSeconds: number): Partial<DeliveryQuery> {
  return { from: bucket.at, to: new Date(new Date(bucket.at).getTime() + bucketSeconds * 1000).toISOString() };
}

/** Says in words what the table shows, so a chart click is never a silent change. */
export function describeQuery(query: DeliveryQuery): string {
  const parts: string[] = [];
  if (query.channel) parts.push(formatChannel(query.channel));
  if (query.variant) parts.push(`Version ${query.variant}`);
  if (query.status) parts.push(deliveryStatusMeta[query.status as DeliveryStatus]?.label ?? query.status);
  if (query.reason) parts.push(`Ended as: ${REASONS[query.reason] ?? query.reason}`);
  if (query.outcome) parts.push(`Had a call: ${OUTCOMES[query.outcome] ?? query.outcome}`);
  if (query.calls) parts.push(CALLS[query.calls] ?? query.calls);
  if (query.city) parts.push(query.city);
  if (query.arrival) parts.push(ARRIVALS[query.arrival]);
  if (query.opened) parts.push(query.opened === "yes" ? "Opened or read" : "Not opened yet");
  if (query.from && query.to) parts.push(`Called between ${formatBucket(query.from, 1)} and ${formatBucket(query.to, 1)}`);
  return parts.join(" · ");
}

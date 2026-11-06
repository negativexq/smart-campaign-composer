import { formatChannel } from "./format";

/** A channel alert on a sending campaign, as the live updates carry it. */
export type ChannelAlert = {
  channel: string;
  kind: string;
  since: string;
  details: Record<string, unknown>;
};

const minutes = (details: Record<string, unknown>) => Number(details.minutes ?? 10);

/** What an open alert says: what is wrong with the channel and what happens meanwhile. */
export function describeAlert(alert: Pick<ChannelAlert, "channel" | "kind" | "details">): { title: string; detail: string } {
  const channel = formatChannel(alert.channel);
  const details = alert.details;
  switch (alert.kind) {
    case "credentials_refused":
      return {
        title: `${channel}: the provider refused our credentials`,
        detail: `${channel} messages wait without using their attempts. One message tests the credentials every 30 seconds, and sending carries on by itself once they are accepted.`,
      };
    case "error_rate":
      return {
        title: `${channel}: most recent calls failed`,
        detail: `${Number(details.failed ?? 0).toLocaleString("en-US")} of the last ${Number(details.calls ?? 0).toLocaleString("en-US")} calls failed. The channel slows down so messages keep their attempts, and goes back to full speed when the provider recovers.`,
      };
    case "no_progress":
      return {
        title: `${channel}: nothing accepted in the last ${minutes(details)} minutes`,
        detail: `${channel} messages are waiting to be tried again. Nothing is lost, and sending carries on when the provider can be reached.`,
      };
    default:
      return { title: `${channel}: ${alert.kind}`, detail: "" };
  }
}

const CLEARED: Record<string, string> = {
  credentials_refused: "credentials accepted again",
  error_rate: "calls going through again",
  no_progress: "no longer stuck",
};

/** What a closed alert says. */
export function describeCleared(alert: Pick<ChannelAlert, "channel" | "kind">): string {
  return `${formatChannel(alert.channel)}: ${CLEARED[alert.kind] ?? "back to normal"}`;
}

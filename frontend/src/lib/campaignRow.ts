import type { CampaignSummary, RunSummary } from "../api/campaigns";
import { formatDateTime } from "./format";

const count = (value: number) => value.toLocaleString("en-US");

/** What kind of campaign it is, in the words of its rule. */
export function kindOf(campaign: Pick<CampaignSummary, "schedule">): string {
  if (campaign.schedule?.stopped) return "Schedule stopped";
  if (campaign.schedule?.repeats) return campaign.schedule.description;
  return campaign.schedule ? "One time, at a set time" : "One time";
}

/** How a run ended, or how far it is. */
export function runResult(run: RunSummary): string {
  if (!run.finished_at) return run.total ? `${Math.floor((run.processed / run.total) * 100)}% done` : "Starting";
  return `${count(run.sent)} sent`;
}

/** Which run, when, and what failed. */
export function runDetail(campaign: Pick<CampaignSummary, "schedule">, run: RunSummary): string {
  const parts = [];
  if (campaign.schedule?.repeats || run.number > 1) parts.push(`Run #${run.number}`);
  parts.push(run.finished_at ? formatDateTime(run.started_at) : `${count(run.processed)} of ${count(run.total)}`);
  if (run.failed) parts.push(`${count(run.failed)} failed`);
  return parts.join(" · ");
}

/**
 * The line a row shows of the messages: the SMS when the campaign texts, since that
 * is the whole message, otherwise the email's subject.
 */
export function previewOf(campaign: Pick<CampaignSummary, "channels" | "preview">): { label: string; text: string } | null {
  const preview = campaign.preview;
  if (!preview) return null;
  const sms = preview.sms.trim();
  const subject = preview.email_subject.trim();
  if (sms && (campaign.channels.includes("sms") || !subject)) return { label: "SMS", text: sms };
  if (subject) return { label: "Subject", text: subject };
  return null;
}

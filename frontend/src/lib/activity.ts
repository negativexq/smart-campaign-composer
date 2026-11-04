import type { ActivityEntry } from "../api/campaigns";
import { languageName } from "./languages";

const FAILURE_REASONS: Record<string, string> = {
  invalid_output: "the output never passed validation",
  refused: "the AI declined the request",
  truncated: "the response was cut off",
  rate_limited: "the AI service was rate limited",
  unavailable: "the AI service was unavailable",
};

const FIELD_NAMES: Record<string, string> = {
  sms: "SMS",
  email_subject: "email subject",
  email_preheader: "preview text",
  email_headline: "headline",
  email_body: "email body",
  email_cta_label: "button label",
  email_cta_url: "button link",
  email_template: "email template",
};

const TRIGGER_TITLES: Record<string, string> = {
  refinement: "Audience refined",
  brief_edit: "Brief edited, audience proposed again",
};

function plural(count: number, noun: string) {
  return `${count.toLocaleString("en-US")} ${noun}${count === 1 ? "" : "s"}`;
}

function queueDetail(details: Record<string, unknown>) {
  return `${plural(Number(details.queued ?? 0), "message")} queued, ${Number(details.skipped ?? 0).toLocaleString("en-US")} skipped`;
}

function finishDetail(details: Record<string, unknown>) {
  const count = (key: string) => Number(details[key] ?? 0).toLocaleString("en-US");
  return `${count("accepted")} accepted, ${count("failed")} failed, ${count("unknown")} unknown, ${count("skipped")} skipped`;
}

const PAUSE_REASONS: Record<string, string> = {
  rate_limited: "The provider kept asking us to slow down",
  provider_unreachable: "The provider could not be reached",
  server_error: "The provider kept returning errors",
  timeout: "The provider stopped answering",
};

function pauseDetail(details: Record<string, unknown>) {
  const why = PAUSE_REASONS[String(details.reason)] ?? "Nothing finished";
  return `${why} for ${Number(details.after_minutes ?? 0)} minutes. ${plural(Number(details.waiting ?? 0), "message")} waiting, none lost.`;
}

/** How a notification reads at a glance: a problem, an end or a start. */
export function notificationTone(entry: ActivityEntry): "error" | "done" | "started" {
  const { failed, unknown } = entry.details;
  if (entry.type === "campaign.paused" || entry.type === "content.generation_failed") return "error";
  if (entry.type === "campaign.run") return "started";
  return Number(failed ?? 0) + Number(unknown ?? 0) > 0 ? "error" : "done";
}

// Who an event belongs to. Older records have no name, and that stays visible.
export function describeActor(entry: ActivityEntry): string {
  if (entry.actor_type === "user") return `by ${entry.actor}`;
  if (entry.actor_type !== "system") return "person not recorded";
  const scheduledBy = entry.details.scheduled_by;
  return scheduledBy ? `by the system, set up by ${scheduledBy}` : "by the system";
}

// Turns an audit event into a sentence. Events this version does not know still
// show up, because the trail must never silently lose a record.
export function describeActivity(entry: ActivityEntry): { title: string; detail: string } {
  const details = entry.details;
  switch (entry.type) {
    case "campaign.created":
      return { title: "Campaign created", detail: "" };
    case "campaign.duplicated":
      return { title: `Copied from "${details.source_name}"`, detail: "" };
    case "deliveries.exported":
      return { title: "Deliveries exported as CSV", detail: "" };
    case "campaign.languages_changed": {
      const excluded = (details.excluded as string[] | undefined) ?? [];
      return { title: "Message languages changed", detail: excluded.length ? `English for readers of ${excluded.map(languageName).join(", ")}` : "Every language gets its own version" };
    }
    case "campaign.renamed":
      return { title: `Renamed to "${details.name}"`, detail: "" };
    case "segment.proposed": {
      const audience = details.audience as { total: number } | undefined;
      const parts = [audience ? `${plural(audience.total, "matching customer")}` : "", `by ${details.provider}`];
      const blocked = Number(details.unsupported_conditions ?? 0);
      if (blocked) parts.push(`${plural(blocked, "unsupported condition")}`);
      return {
        title: TRIGGER_TITLES[String(details.trigger)] ?? "Audience proposed",
        detail: parts.filter(Boolean).join(", "),
      };
    }
    case "content.generated": {
      const attempts = Number(details.attempts ?? 1);
      const parts = [`by ${details.provider}`];
      if (attempts > 1) parts.push(`${attempts} attempts`);
      return { title: details.regenerated ? "Messages rewritten" : "Messages written", detail: parts.join(", ") };
    }
    case "content.edited": {
      const fields = (details.fields as string[] | undefined) ?? [];
      return { title: "Messages edited", detail: fields.map((field) => FIELD_NAMES[field] ?? field).join(", ") };
    }
    case "content.generation_failed":
      return { title: "Writing messages failed", detail: FAILURE_REASONS[String(details.reason)] ?? "" };
    case "campaign.approved":
      return { title: "Approved", detail: "Audience rules and messages are now fixed" };
    case "campaign.scheduled":
      return { title: "Scheduled", detail: String(details.schedule ?? "") };
    case "campaign.schedule_cancelled":
      return { title: "Schedule cancelled", detail: "" };
    case "campaign.dispatched":
      return { title: "Sending started", detail: queueDetail(details) };
    case "campaign.run":
      return { title: "Scheduled send started", detail: queueDetail(details) };
    case "campaign.run_finished":
      return { title: "Scheduled send finished", detail: finishDetail(details) };
    case "campaign.completed":
      return { title: "Campaign finished", detail: finishDetail(details) };
    case "campaign.paused":
      return { title: "Sending stopped", detail: pauseDetail(details) };
    case "campaign.resumed":
      return { title: "Sending resumed", detail: "" };
    default:
      return { title: entry.type, detail: "" };
  }
}

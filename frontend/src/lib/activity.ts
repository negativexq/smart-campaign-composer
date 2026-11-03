import type { ActivityEntry } from "../api/campaigns";

const FAILURE_REASONS: Record<string, string> = {
  invalid_output: "the output never passed validation",
  refused: "the AI declined the request",
  truncated: "the response was cut off",
  rate_limited: "the AI service was rate limited",
  unavailable: "the AI service was unavailable",
};

const FIELD_NAMES: Record<string, string> = { sms: "SMS", email_subject: "email subject", email_body: "email body" };

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

// Turns an audit event into a sentence. Events this version does not know still
// show up, because the trail must never silently lose a record.
export function describeActivity(entry: ActivityEntry): { title: string; detail: string } {
  const details = entry.details;
  switch (entry.type) {
    case "campaign.created":
      return { title: "Campaign created", detail: "" };
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
    default:
      return { title: entry.type, detail: "" };
  }
}

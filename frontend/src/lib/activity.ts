import type { ActivityEntry } from "../api/campaigns";

const TRIGGER_TITLES: Record<string, string> = {
  refinement: "Audience refined",
  brief_edit: "Brief edited, audience proposed again",
};

function plural(count: number, noun: string) {
  return `${count.toLocaleString("en-US")} ${noun}${count === 1 ? "" : "s"}`;
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
    default:
      return { title: entry.type, detail: "" };
  }
}

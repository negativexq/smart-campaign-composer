import type { ScheduleReach, ScheduleSpec } from "../api/campaigns";

export type ScheduleMode = "now" | "once" | "interval" | "weekly";
export type IntervalUnit = "hours" | "days";

export type ScheduleForm = {
  mode: ScheduleMode;
  at: string;
  every: number;
  unit: IntervalUnit;
  startsAt: string;
  days: string[];
  time: string;
  timezone: string;
  endsAt: string;
  reach: ScheduleReach;
  reachAfterDays: number;
};

export const WEEKDAYS = [
  { id: "mon", label: "Mon" },
  { id: "tue", label: "Tue" },
  { id: "wed", label: "Wed" },
  { id: "thu", label: "Thu" },
  { id: "fri", label: "Fri" },
  { id: "sat", label: "Sat" },
  { id: "sun", label: "Sun" },
];

export function browserTimezone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
}

export function emptyScheduleForm(): ScheduleForm {
  return {
    mode: "now",
    at: "",
    every: 24,
    unit: "hours",
    startsAt: "",
    days: [],
    time: "09:00",
    timezone: browserTimezone(),
    endsAt: "",
    reach: "new",
    reachAfterDays: 7,
  };
}

// A sensible rest before someone is reached again: one interval, or a week for
// sends on chosen days.
export function suggestedRestDays(form: ScheduleForm): number {
  if (form.mode !== "interval") return 7;
  const hours = form.unit === "days" ? form.every * 24 : form.every;
  return Math.min(365, Math.max(1, Math.round(hours / 24)));
}

function reachOf(form: ScheduleForm): Pick<ScheduleSpec, "reach" | "reach_after_days"> {
  if (form.reach === "after") return { reach: "after", reach_after_days: form.reachAfterDays };
  return form.reach === "everyone" ? { reach: "everyone" } : {};
}

// datetime-local gives a time without a zone, which means the browser's own.
// The offset is kept so the backend can describe the time as it was chosen.
function toInstant(local: string): string | undefined {
  if (!local) return undefined;
  const parsed = new Date(local);
  if (Number.isNaN(parsed.getTime())) return undefined;
  const offset = -parsed.getTimezoneOffset();
  const pad = (value: number) => String(Math.floor(Math.abs(value))).padStart(2, "0");
  return `${local.slice(0, 16)}:00${offset < 0 ? "-" : "+"}${pad(offset / 60)}:${pad(offset % 60)}`;
}

// A one-time send of a campaign that sent before says whom it reaches, even the default.
function explicitReach(form: ScheduleForm): Pick<ScheduleSpec, "reach" | "reach_after_days"> {
  return form.reach === "after" ? reachOf(form) : { reach: form.reach };
}

// What is still missing before the form can be sent. The backend has the final say.
// sentBefore means the campaign already sent, so a one-time send chooses whom it reaches.
export function scheduleProblem(form: ScheduleForm, sentBefore = false): string {
  switch (form.mode) {
    case "once":
      if (!toInstant(form.at)) return "Choose a date and time.";
      return sentBefore ? restProblem(form) : "";
    case "interval":
      if (!Number.isInteger(form.every) || form.every < 1) return "Choose how often to send.";
      return restProblem(form);
    case "weekly":
      if (!form.days.length) return "Choose at least one day.";
      return form.time ? restProblem(form) : "Choose a time.";
    default:
      return "";
  }
}

function restProblem(form: ScheduleForm): string {
  if (form.reach !== "after") return "";
  return Number.isInteger(form.reachAfterDays) && form.reachAfterDays >= 1 && form.reachAfterDays <= 365
    ? ""
    : "Choose a waiting time from 1 to 365 days.";
}

// Null means send now.
export function buildSchedule(form: ScheduleForm, sentBefore = false): ScheduleSpec | null {
  const ends = toInstant(form.endsAt);
  switch (form.mode) {
    case "once":
      return { kind: "once", at: toInstant(form.at), ...(sentBefore ? explicitReach(form) : {}) };
    case "interval":
      return {
        kind: "interval",
        every_hours: form.unit === "days" ? form.every * 24 : form.every,
        ...(toInstant(form.startsAt) ? { starts_at: toInstant(form.startsAt) } : {}),
        ...(ends ? { ends_at: ends } : {}),
        ...reachOf(form),
      };
    case "weekly":
      return { kind: "weekly", days: form.days, time: form.time, timezone: form.timezone, ...(ends ? { ends_at: ends } : {}), ...reachOf(form) };
    default:
      return null;
  }
}

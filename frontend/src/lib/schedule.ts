import type { ScheduleSpec } from "../api/campaigns";

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
  };
}

// datetime-local gives a time without a zone, which means the browser's own.
function toInstant(local: string): string | undefined {
  if (!local) return undefined;
  const parsed = new Date(local);
  return Number.isNaN(parsed.getTime()) ? undefined : parsed.toISOString();
}

// What is still missing before the form can be sent. The backend has the final say.
export function scheduleProblem(form: ScheduleForm): string {
  switch (form.mode) {
    case "once":
      return toInstant(form.at) ? "" : "Choose a date and time.";
    case "interval":
      return Number.isInteger(form.every) && form.every >= 1 ? "" : "Choose how often to send.";
    case "weekly":
      if (!form.days.length) return "Choose at least one day.";
      return form.time ? "" : "Choose a time.";
    default:
      return "";
  }
}

// Null means send now.
export function buildSchedule(form: ScheduleForm): ScheduleSpec | null {
  const ends = toInstant(form.endsAt);
  switch (form.mode) {
    case "once":
      return { kind: "once", at: toInstant(form.at) };
    case "interval":
      return {
        kind: "interval",
        every_hours: form.unit === "days" ? form.every * 24 : form.every,
        ...(toInstant(form.startsAt) ? { starts_at: toInstant(form.startsAt) } : {}),
        ...(ends ? { ends_at: ends } : {}),
      };
    case "weekly":
      return { kind: "weekly", days: form.days, time: form.time, timezone: form.timezone, ...(ends ? { ends_at: ends } : {}) };
    default:
      return null;
  }
}

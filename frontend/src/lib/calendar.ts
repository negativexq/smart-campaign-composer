import type { CalendarEntry, CalendarEntryKind } from "../api/campaigns";

export type CalendarView = "day" | "week" | "month";

export const CALENDAR_VIEWS: { view: CalendarView; label: string }[] = [
  { view: "day", label: "Day" },
  { view: "week", label: "Week" },
  { view: "month", label: "Month" },
];

/** What each kind of entry is called, in the legend and in what is read aloud. */
export const ENTRY_LABELS: Record<CalendarEntryKind, string> = {
  draft: "Draft",
  scheduled: "Scheduled",
  sending: "Sending",
  sent: "Completed",
};

export function startOfDay(date: Date): Date {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate());
}

export function addDays(date: Date, days: number): Date {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate() + days);
}

/** Weeks start on Monday, as they do where the product is used. */
export function startOfWeek(date: Date): Date {
  const day = startOfDay(date);
  return addDays(day, -((day.getDay() + 6) % 7));
}

/** The days a view shows around anchor, in local time. A month is whole weeks. */
export function daysOf(view: CalendarView, anchor: Date): Date[] {
  if (view === "day") return [startOfDay(anchor)];
  if (view === "week") return Array.from({ length: 7 }, (_, index) => addDays(startOfWeek(anchor), index));
  const first = new Date(anchor.getFullYear(), anchor.getMonth(), 1);
  const last = new Date(anchor.getFullYear(), anchor.getMonth() + 1, 0);
  const start = startOfWeek(first);
  const end = addDays(startOfWeek(last), 7);
  const days: Date[] = [];
  for (let day = start; day < end; day = addDays(day, 1)) days.push(day);
  return days;
}

/** The range to ask the server for: from the first shown day to the day after the last. */
export function rangeOf(view: CalendarView, anchor: Date): { from: Date; to: Date } {
  const days = daysOf(view, anchor);
  return { from: days[0], to: addDays(days[days.length - 1], 1) };
}

/** The anchor one view later, or earlier when step is negative. */
export function shift(view: CalendarView, anchor: Date, step: number): Date {
  if (view === "day") return addDays(anchor, step);
  if (view === "week") return addDays(anchor, 7 * step);
  return new Date(anchor.getFullYear(), anchor.getMonth() + step, 1);
}

export function titleOf(view: CalendarView, anchor: Date): string {
  if (view === "day")
    return anchor.toLocaleDateString("en-GB", {
      weekday: "long",
      day: "numeric",
      month: "long",
      year: "numeric",
    });
  if (view === "month")
    return anchor.toLocaleDateString("en-GB", {
      month: "long",
      year: "numeric",
    });
  const days = daysOf("week", anchor);
  const first = days[0];
  const last = days[6];
  const end = last.toLocaleDateString("en-GB", {
    day: "numeric",
    month: "short",
    year: "numeric",
  });
  if (first.getMonth() === last.getMonth()) return `${first.getDate()} – ${end}`;
  const sameYear = first.getFullYear() === last.getFullYear();
  return `${first.toLocaleDateString("en-GB", sameYear ? { day: "numeric", month: "short" } : { day: "numeric", month: "short", year: "numeric" })} – ${end}`;
}

/** A local day as text, to group entries by. */
export function dayKey(date: Date): string {
  const pad = (value: number) => String(value).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
}

export function entriesByDay(entries: CalendarEntry[]): Map<string, CalendarEntry[]> {
  const days = new Map<string, CalendarEntry[]>();
  for (const entry of [...entries].sort((a, b) => a.at.localeCompare(b.at))) {
    const key = dayKey(new Date(entry.at));
    days.set(key, [...(days.get(key) ?? []), entry]);
  }
  return days;
}

export function formatTime(value: string): string {
  return new Date(value).toLocaleTimeString("en-GB", {
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** A finished run that lost messages is shown as needing a look, not as clean. */
export function hasFailures(entry: CalendarEntry): boolean {
  return Boolean(entry.run && (entry.run.failed || entry.run.unknown));
}

/** The anchor kept in the address, or today when it names none. */
export function parseAnchor(value: string | null): Date {
  const match = value?.match(/^(\d{4})-(\d{2})-(\d{2})$/);
  if (!match) return startOfDay(new Date());
  const date = new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]));
  return Number.isNaN(date.getTime()) ? startOfDay(new Date()) : date;
}

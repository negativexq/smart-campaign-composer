import { describe, expect, it } from "vitest";

import { buildSchedule, emptyScheduleForm, scheduleProblem, suggestedRestDays } from "./schedule";

describe("schedule form", () => {
  it("means send now by default", () => {
    expect(buildSchedule(emptyScheduleForm())).toBeNull();
    expect(scheduleProblem(emptyScheduleForm())).toBe("");
  });

  it("needs a time for a one-time send and turns it into an instant", () => {
    const form = { ...emptyScheduleForm(), mode: "once" as const };
    expect(scheduleProblem(form)).toContain("date");
    form.at = "2026-11-05T09:00";
    expect(scheduleProblem(form)).toBe("");
    const spec = buildSchedule(form);
    expect(spec?.kind).toBe("once");
    expect(spec?.at).toMatch(/^2026-11-05T09:00:00[+-]\d\d:\d\d$/);
    expect(new Date(spec?.at ?? "").getTime()).toBe(new Date("2026-11-05T09:00").getTime());
  });

  it("expresses days as hours for the backend", () => {
    const form = { ...emptyScheduleForm(), mode: "interval" as const, every: 2, unit: "days" as const };
    expect(buildSchedule(form)).toEqual({ kind: "interval", every_hours: 48 });
  });

  it("rejects an interval that is not a whole positive number", () => {
    for (const every of [0, -1, 1.5]) {
      expect(scheduleProblem({ ...emptyScheduleForm(), mode: "interval", every })).not.toBe("");
    }
  });

  it("needs days for a weekly rule and keeps its time zone", () => {
    const form = { ...emptyScheduleForm(), mode: "weekly" as const, timezone: "Europe/Istanbul" };
    expect(scheduleProblem(form)).toContain("day");
    form.days = ["mon", "fri"];
    expect(scheduleProblem(form)).toBe("");
    expect(buildSchedule(form)).toEqual({ kind: "weekly", days: ["mon", "fri"], time: "09:00", timezone: "Europe/Istanbul" });
  });

  it("includes an end only when one is chosen", () => {
    const form = { ...emptyScheduleForm(), mode: "weekly" as const, days: ["mon"], endsAt: "2026-12-01T00:00" };
    expect(new Date(buildSchedule(form)?.ends_at ?? "").getTime()).toBe(new Date("2026-12-01T00:00").getTime());
  });

  it("sends whom later runs reach only when it is not the default", () => {
    const form = { ...emptyScheduleForm(), mode: "interval" as const, every: 3, unit: "days" as const };
    expect(buildSchedule(form)).not.toHaveProperty("reach");
    expect(buildSchedule({ ...form, reach: "after", reachAfterDays: 3 })).toMatchObject({ reach: "after", reach_after_days: 3 });
    expect(buildSchedule({ ...form, reach: "everyone" })).toEqual({ kind: "interval", every_hours: 72, reach: "everyone" });
  });

  it("asks for a waiting time from 1 to 365 days", () => {
    const form = { ...emptyScheduleForm(), mode: "interval" as const, reach: "after" as const };
    for (const reachAfterDays of [0, 366, 2.5]) {
      expect(scheduleProblem({ ...form, reachAfterDays })).not.toBe("");
    }
    expect(scheduleProblem({ ...form, reachAfterDays: 30 })).toBe("");
  });

  it("suggests waiting one interval, or a week for chosen days", () => {
    const form = emptyScheduleForm();
    expect(suggestedRestDays({ ...form, mode: "interval", every: 14, unit: "days" })).toBe(14);
    expect(suggestedRestDays({ ...form, mode: "interval", every: 6, unit: "hours" })).toBe(1);
    expect(suggestedRestDays({ ...form, mode: "weekly" })).toBe(7);
  });
});

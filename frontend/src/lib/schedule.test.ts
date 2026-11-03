import { describe, expect, it } from "vitest";

import { buildSchedule, emptyScheduleForm, scheduleProblem } from "./schedule";

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
    expect(buildSchedule(form)).toEqual({ kind: "once", at: new Date("2026-11-05T09:00").toISOString() });
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
    expect(buildSchedule(form)?.ends_at).toBe(new Date("2026-12-01T00:00").toISOString());
  });
});

import { describe, expect, it } from "vitest";

import { dayKey, daysOf, entriesByDay, parseAnchor, rangeOf, shift, titleOf } from "./calendar";

const day = (year: number, month: number, date: number) => new Date(year, month - 1, date);

describe("calendar", () => {
  it("lays a month out in whole weeks that start on Monday", () => {
    const days = daysOf("month", day(2026, 11, 18));
    expect(dayKey(days[0])).toBe("2026-10-26");
    // November 2026 starts on a Sunday and ends on a Monday, so it needs six weeks.
    expect(dayKey(days[days.length - 1])).toBe("2026-12-06");
    expect(days).toHaveLength(42);
    expect(daysOf("month", day(2026, 10, 1))).toHaveLength(35);
  });

  it("asks for the shown days and nothing past them", () => {
    const { from, to } = rangeOf("week", day(2026, 11, 11));
    expect(dayKey(from)).toBe("2026-11-09");
    expect(dayKey(to)).toBe("2026-11-16");
    const single = rangeOf("day", new Date(2026, 10, 11, 15, 30));
    expect(single.from.getHours()).toBe(0);
    expect(dayKey(single.to)).toBe("2026-11-12");
  });

  it("moves by the view's own step", () => {
    expect(dayKey(shift("day", day(2026, 11, 30), 1))).toBe("2026-12-01");
    expect(dayKey(shift("week", day(2026, 11, 30), -1))).toBe("2026-11-23");
    expect(dayKey(shift("month", day(2026, 1, 31), 1))).toBe("2026-02-01");
  });

  it("names the range it shows", () => {
    expect(titleOf("month", day(2026, 11, 9))).toBe("November 2026");
    expect(titleOf("week", day(2026, 11, 11))).toBe("9 – 15 Nov 2026");
    expect(titleOf("week", day(2026, 12, 30))).toBe("28 Dec 2026 – 3 Jan 2027");
  });

  it("groups entries by local day, earliest first", () => {
    const at = (date: Date) => date.toISOString();
    const grouped = entriesByDay([
      { campaign_id: "b", kind: "scheduled", at: at(new Date(2026, 10, 9, 18)) },
      { campaign_id: "a", kind: "draft", at: at(new Date(2026, 10, 9, 8)) },
      { campaign_id: "c", kind: "sent", at: at(new Date(2026, 10, 10, 0, 5)) },
    ]);
    expect(grouped.get("2026-11-09")?.map((entry) => entry.campaign_id)).toEqual(["a", "b"]);
    expect(grouped.get("2026-11-10")).toHaveLength(1);
  });

  it("reads the day from the address and falls back to today", () => {
    expect(dayKey(parseAnchor("2026-11-09"))).toBe("2026-11-09");
    expect(dayKey(parseAnchor("soon"))).toBe(dayKey(new Date()));
    expect(dayKey(parseAnchor(null))).toBe(dayKey(new Date()));
  });
});

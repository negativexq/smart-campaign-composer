import { describe, expect, it } from "vitest";

import type { Progress } from "../api/campaigns";
import { applySelection, arrivalOf, bucketQuery, channelBars, describeQuery, formatBucketWidth, matches, niceMax, noFilter, queryFromSearch, searchWithQuery } from "./report";

describe("channelBars", () => {
  const progress = (byChannel: Progress["by_channel"]) => ({ by_channel: byChannel }) as unknown as Progress;

  it("leaves out a channel the campaign did not use", () => {
    const bars = channelBars(progress({ email: { SENT: 3 } }));
    expect(bars.map((bar) => bar.id)).toEqual(["email"]);
  });

  it("joins everything still in the queue into one waiting segment and drops zeroes", () => {
    const [bar] = channelBars(progress({ sms: { SENT: 1, QUEUED: 2, SENDING: 3, RETRYING: 4 } }));
    expect(bar.total).toBe(10);
    expect(bar.segments.map((segment) => [segment.status, segment.count])).toEqual([["SENT", 1], ["WAITING", 9]]);
  });
});

describe("axis and buckets", () => {
  it.each([[0, 1], [3, 3], [7, 10], [12, 20], [230, 500], [1000, 1000]])("rounds %i up to %i", (value, want) => {
    expect(niceMax(value)).toBe(want);
  });

  it("ends a bucket's filter where the next one starts", () => {
    const bucket = { at: "2026-11-03T12:00:50Z", accepted: 1, rate_limited: 0, server_error: 0, rejected: 0, timeout: 0 };
    expect(bucketQuery(bucket, 10)).toEqual({ from: "2026-11-03T12:00:50Z", to: "2026-11-03T12:01:00.000Z" });
  });

  it.each([[1, "second"], [5, "5 seconds"], [60, "minute"], [300, "5 minutes"], [3600, "hour"], [86400, "day"]])("names %i seconds", (seconds, want) => {
    expect(formatBucketWidth(seconds)).toBe(want);
  });
});

describe("filters", () => {
  it("describes opened in words", () => {
    expect(describeQuery({ ...noFilter(), opened: "no" })).toBe("Not opened yet");
  });

  it("recognises only the exact selection as already active", () => {
    const query = { ...noFilter(), channel: "sms", status: "FAILED" };
    expect(matches(query, { status: "FAILED", channel: "sms" })).toBe(true);
    expect(matches(query, { status: "FAILED" })).toBe(false);
  });

  it("describes a filter in words", () => {
    expect(describeQuery({ ...noFilter(), channel: "email", reason: "timeout", calls: "2+" })).toBe(
      "Email · Ended as: No response from the provider · Called more than once",
    );
  });
});

describe("arrivalOf", () => {
  it("reads what the provider reported", () => {
    expect(arrivalOf({ status: "SENT", delivered_at: "2026-11-03T10:00:00Z" })).toEqual({ label: "Delivered", at: "2026-11-03T10:00:00Z" });
    expect(arrivalOf({ status: "SENT", bounced_at: "2026-11-03T10:00:00Z" })?.label).toBe("Bounced");
    expect(arrivalOf({ status: "UNKNOWN", delivered_at: "2026-11-03T10:00:00Z" })?.label).toBe("Delivered");
  });

  it("only accepted messages wait for a report", () => {
    expect(arrivalOf({ status: "SENT" })).toEqual({ label: "Waiting for a report", at: null });
    expect(arrivalOf({ status: "FAILED" })).toBeNull();
  });
});

describe("chart selections", () => {
  it("replaces the earlier selection and keeps what no chart selects by", () => {
    const query = { ...noFilter(), city: "Ankara", calls: "2+", outcome: "timeout" };
    expect(applySelection(query, { channel: "sms", status: "SENT" })).toEqual({ ...noFilter(), city: "Ankara", calls: "2+", channel: "sms", status: "SENT" });
  });

  it("clears only the selection when it is clicked again, and still shows as selected", () => {
    const query = { ...noFilter(), city: "Ankara", reason: "timeout" };
    expect(matches(query, { reason: "timeout" })).toBe(true);
    expect(applySelection(query, { reason: "timeout" })).toEqual({ ...noFilter(), city: "Ankara" });
  });

  it("does not count a filter set by hand as the chart's selection", () => {
    expect(matches({ ...noFilter(), status: "SENT", channel: "sms" }, { channel: "sms" })).toBe(false);
  });
});

describe("filters in the address", () => {
  it("reads the filters and nothing else", () => {
    const query = queryFromSearch("?tab=deliveries&city=Ankara&status=SENT&utm=x");
    expect(query).toEqual({ ...noFilter(), city: "Ankara", status: "SENT" });
  });

  it("writes them beside what is already there and removes those cleared", () => {
    expect(searchWithQuery("?tab=deliveries&status=SENT", { ...noFilter(), calls: "2+" })).toBe("?tab=deliveries&calls=2%2B");
    expect(searchWithQuery("?status=SENT", noFilter())).toBe("");
  });

  it("comes back the same, a time range included", () => {
    const query = { ...noFilter(), from: "2026-11-03T12:00:00Z", to: "2026-11-03T12:00:10Z", channel: "email" };
    expect(queryFromSearch(searchWithQuery("?tab=deliveries", query))).toEqual(query);
  });
});

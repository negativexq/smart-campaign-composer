import { describe, expect, it } from "vitest";

import type { Progress } from "../api/campaigns";
import { bucketQuery, channelBars, describeQuery, formatBucketWidth, matches, niceMax, noFilter } from "./report";

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
    expect(describeQuery({ ...noFilter(), channel: "email", reason: "timeout", retried: true })).toBe(
      "Email · Ended as: No response from the provider · Called more than once",
    );
  });
});

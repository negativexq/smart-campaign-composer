import { describe, expect, it } from "vitest";

import { DELIVERY_STATUSES, deliveryStatusMeta } from "./deliveryStatus";

describe("deliveryStatusMeta", () => {
  it("covers every status the database allows", () => {
    expect([...DELIVERY_STATUSES].sort()).toEqual(
      ["FAILED", "QUEUED", "RETRYING", "SENDING", "SENT", "SKIPPED", "UNKNOWN"],
    );
  });

  it("gives every status a label, icon and description", () => {
    for (const status of DELIVERY_STATUSES) {
      const meta = deliveryStatusMeta[status];
      expect(meta.label).not.toBe("");
      expect(meta.icon).toBeTruthy();
      expect(meta.description).not.toBe("");
    }
  });

  it("presents SENT as provider acceptance, not delivery", () => {
    expect(deliveryStatusMeta.SENT.label).toBe("Accepted");
    expect(deliveryStatusMeta.SENT.description.toLowerCase()).toContain("not");
  });

  it("keeps an unconfirmed send apart from a failure", () => {
    expect(deliveryStatusMeta.UNKNOWN.label).not.toBe(deliveryStatusMeta.FAILED.label);
    expect(deliveryStatusMeta.UNKNOWN.tone).not.toBe("error");
  });
});

import { describe, expect, it } from "vitest";

import { countCharacters, findUnknownVariables, reachHint, renderMessage, smsSegments } from "./message";

describe("countCharacters", () => {
  it("counts Turkish letters as one character each", () => {
    expect(countCharacters("Şükrü")).toBe(5);
  });

  it("counts an emoji as one character, matching the backend", () => {
    expect(countCharacters("🎉")).toBe(1);
  });

  it("counts an empty message as zero", () => {
    expect(countCharacters("")).toBe(0);
  });
});

describe("renderMessage", () => {
  it("fills in the first name", () => {
    expect(renderMessage("Hi {{first_name}}!", { first_name: "Ayşe" })).toBe("Hi Ayşe!");
  });

  it("accepts spaces inside the braces", () => {
    expect(renderMessage("Hi {{ first_name }}!", { first_name: "Ayşe" })).toBe("Hi Ayşe!");
  });

  it("uses the neutral fallback when the name is empty", () => {
    expect(renderMessage("Hi {{first_name}}!", { first_name: "" })).toBe("Hi there!");
  });

  it("leaves unknown variables visible instead of hiding the mistake", () => {
    expect(renderMessage("Use {{coupon}}", { first_name: "Ayşe" })).toBe("Use {{coupon}}");
  });
});

describe("findUnknownVariables", () => {
  it("accepts supported variables", () => {
    expect(findUnknownVariables("Hi {{first_name}}")).toEqual([]);
  });

  it("reports each unsupported variable once", () => {
    expect(findUnknownVariables("{{coupon}} {{ coupon }} {{city_name}}")).toEqual(["coupon", "city_name"]);
  });
});

describe("smsSegments", () => {
  it("fits 160 GSM-7 characters in one segment and splits at 153 after that", () => {
    expect(smsSegments("a".repeat(160))).toEqual({ encoding: "GSM-7", segments: 1 });
    expect(smsSegments("a".repeat(161))).toEqual({ encoding: "GSM-7", segments: 2 });
    expect(smsSegments("a".repeat(306))).toEqual({ encoding: "GSM-7", segments: 2 });
    expect(smsSegments("a".repeat(307))).toEqual({ encoding: "GSM-7", segments: 3 });
  });

  it("counts extended characters twice", () => {
    expect(smsSegments("€".repeat(80)).segments).toBe(1);
    expect(smsSegments("€".repeat(81)).segments).toBe(2);
  });

  it("moves the whole message to Unicode for one Turkish letter", () => {
    expect(smsSegments("ş".repeat(70))).toEqual({ encoding: "Unicode", segments: 1 });
    expect(smsSegments("ş".repeat(71))).toEqual({ encoding: "Unicode", segments: 2 });
    expect(smsSegments("ı" + "a".repeat(159))).toEqual({ encoding: "Unicode", segments: 3 });
  });

  it("has nothing to send for an empty message", () => {
    expect(smsSegments("").segments).toBe(0);
  });
});

describe("reachHint", () => {
  it("names who is missed and why", () => {
    expect(reachHint(467, 330, "SMS")).toBe("137 not reachable: no SMS consent");
    expect(reachHint(10, 10, "email")).toBe("Everyone matching consented to email");
  });
});

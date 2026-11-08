import { describe, expect, it } from "vitest";

import {
  briefCodes,
  campaignDiscount,
  countCharacters,
  findUnknownVariables,
  hasCode,
  hasLink,
  longestRenderedLength,
  reachHint,
  renderMessage,
  SHORT_LINK_LENGTH,
  smsSegments,
  validCode,
} from "./message";

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

describe("campaignDiscount", () => {
  it("prefers the form, falls back to the brief and may be empty", () => {
    expect(campaignDiscount({ offer_percent: 25, brief: "30% off" })).toBe("25%");
    expect(campaignDiscount({ offer_percent: null, brief: "Send %30 off" })).toBe("%30");
    expect(campaignDiscount({ brief: "Remind them" })).toBe("");
  });
});

describe("a missing first name", () => {
  it("is replaced in the reader's language", () => {
    expect(renderMessage("Hi {{first_name}}", { first_name: "" }, "tr")).toBe("Hi müşterimiz");
    expect(renderMessage("Hi {{first_name}}", { first_name: "" }, "en")).toBe("Hi there");
    expect(renderMessage("Hi {{first_name}}", { first_name: "" })).toBe("Hi there");
  });
});

describe("link", () => {
  it("counts as a short link, as the backend counts it", () => {
    expect(SHORT_LINK_LENGTH).toBe("https://url.short/".length + 8);
    expect(longestRenderedLength("Shop: {{link}}", 0)).toBe(6 + SHORT_LINK_LENGTH);
  });

  it("is filled in with the short link and refused without a link in the brief", () => {
    expect(renderMessage("Shop {{ link }}", { first_name: "Ayşe", link: "https://url.short/a1B2c3D4" })).toBe("Shop https://url.short/a1B2c3D4");
    expect(findUnknownVariables("Shop {{link}}")).toEqual(["link"]);
    expect(findUnknownVariables("Shop {{link}}", "", "https://shop.example.com")).toEqual([]);
    expect(hasLink("Shop {{link}}")).toBe(true);
  });
});

describe("discount code", () => {
  it("is filled in and counted as the code", () => {
    expect(renderMessage("Use {{discount_code}}, {{first_name}}", { first_name: "Ayşe", discount_code: "SONBAHAR15" })).toBe("Use SONBAHAR15, Ayşe");
    expect(longestRenderedLength("x{{discount_code}}", 0, "SONBAHAR15")).toBe(11);
  });

  it("stays a variable, and is refused, without a code", () => {
    expect(renderMessage("Use {{discount_code}}", { first_name: "Ayşe" })).toBe("Use {{discount_code}}");
    expect(findUnknownVariables("Use {{discount_code}}")).toEqual(["discount_code"]);
    expect(findUnknownVariables("Use {{discount_code}}", "SONBAHAR15")).toEqual([]);
    expect(hasCode("Use {{ discount_code }}")).toBe(true);
  });

  it("is read from a brief next to a word for one, as the backend reads it", () => {
    expect(briefCodes("%15 indirim kodu SONBAHAR15 ver.")).toEqual(["SONBAHAR15"]);
    expect(briefCodes("SONBAHAR15 kodunu kullan.")).toEqual(["SONBAHAR15"]);
    expect(briefCodes("Promo code: SPRING-10 for everyone.")).toEqual(["SPRING-10"]);
    expect(briefCodes("Kodu A1B ya da kodu C2D olsun.")).toEqual(["A1B", "C2D"]);
    expect(briefCodes("%15 indirim, 15 kodu yok. SONBAHAR15 koleksiyonunu duyur.")).toEqual([]);
  });

  it("is capitals, digits and hyphens", () => {
    expect(validCode("SPRING-10")).toBe(true);
    expect(validCode("spring10")).toBe(false);
    expect(validCode("AB")).toBe(false);
    expect(validCode("SALE 10")).toBe(false);
  });
});

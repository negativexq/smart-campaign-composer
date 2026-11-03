import { describe, expect, it } from "vitest";

import { countCharacters, findUnknownVariables, renderMessage } from "./message";

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

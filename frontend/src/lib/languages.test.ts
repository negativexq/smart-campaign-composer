import { describe, expect, it } from "vitest";

import type { Campaign, LanguageAudience } from "../api/campaigns";
import { audienceLanguages, languageChoices, listNames, longestNameFor, missingLanguages, variantCount } from "./languages";

const people = (language: string, total: number, name = 5): LanguageAudience => ({
  language,
  total,
  sms: total,
  email: total,
  sms_name_length: name,
  email_name_length: name,
});

function campaign(overrides: Partial<Campaign>): Campaign {
  return {
    languages: [people("tr", 400), people("en", 90), people("de", 60), people("fr", 12, 30)],
    excluded_languages: [],
    content_limits: { sms_name_length: 0, email_name_length: 0 },
    ...overrides,
  } as Campaign;
}

describe("message languages", () => {
  it("gives every language its own version by default", () => {
    expect(audienceLanguages(campaign({})).map((version) => version.language)).toEqual(["tr", "en", "de", "fr"]);
  });

  it("counts those left out as English readers without losing anyone", () => {
    const versions = audienceLanguages(campaign({ excluded_languages: ["fr"] }));
    const english = versions.find((version) => version.language === "en");
    expect(versions.map((version) => version.language)).toEqual(["tr", "en", "de"]);
    expect(english).toMatchObject({ total: 102, sms: 102, email: 102, also: ["fr"] });
    expect(versions.reduce((sum, version) => sum + version.total, 0)).toBe(562);
  });

  it("adds English when only those left out need it", () => {
    const versions = audienceLanguages(campaign({ languages: [people("tr", 400), people("de", 60)], excluded_languages: ["de"] }));
    expect(versions.map((version) => version.language)).toEqual(["tr", "en"]);
    expect(versions[1]).toMatchObject({ total: 60, also: ["de"] });
  });

  it("holds English to the longest name that falls back to it", () => {
    expect(longestNameFor(campaign({ excluded_languages: ["fr"] }), "en", "sms")).toBe(30);
    expect(longestNameFor(campaign({}), "en", "sms")).toBe(5);
  });

  it("asks for English when it is needed and not written", () => {
    const content = { language: "tr", translations: [] } as unknown as Campaign["content"];
    const current = campaign({ languages: [people("tr", 400), people("de", 60)], excluded_languages: ["de"], content });
    expect(missingLanguages(current)).toEqual(["en"]);
  });

  it("counts a test's side by the version people read", () => {
    const variant_audience = [
      { variant: "A", language: "en", total: 10, sms: 10, email: 10 },
      { variant: "A", language: "fr", total: 2, sms: 2, email: 2 },
    ];
    expect(variantCount(campaign({ excluded_languages: ["fr"], variant_audience }), "A", { language: "en" })).toBe(12);
  });

  it("offers each language with its people and keeps English on", () => {
    const choices = languageChoices(campaign({ excluded_languages: ["de"] }));
    expect(choices.map((choice) => [choice.language, choice.people, choice.chosen, choice.locked])).toEqual([
      ["tr", 400, true, false],
      ["en", 90, true, true],
      ["de", 60, false, false],
      ["fr", 12, true, false],
    ]);
  });

  it("joins names as a sentence", () => {
    expect(listNames(["A"])).toBe("A");
    expect(listNames(["A", "B", "C"], "or")).toBe("A, B or C");
  });
});

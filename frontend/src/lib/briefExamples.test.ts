import { describe, expect, it } from "vitest";
import { BRIEF_EXAMPLES } from "./briefExamples";
import { briefCodes, briefLinks } from "./message";

describe("the brief examples", () => {
  it("each pass the form as written, with one link and at most one code", () => {
    for (const example of BRIEF_EXAMPLES) {
      expect(briefLinks(example), example).toHaveLength(1);
      expect(briefCodes(example).length, example).toBeLessThanOrEqual(1);
    }
    expect(BRIEF_EXAMPLES.filter((example) => briefCodes(example).length === 1)).toHaveLength(3);
  });
});

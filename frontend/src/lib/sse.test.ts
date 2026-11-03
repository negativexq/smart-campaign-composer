import { describe, expect, it } from "vitest";

import { EventStreamParser } from "./sse";

describe("EventStreamParser", () => {
  it("reads events split across chunks", () => {
    const parser = new EventStreamParser();
    expect(parser.push('event: delta\ndata: {"te')).toEqual([]);
    expect(parser.push('xt":"a"}\n\nevent: done\ndata: {}\n\nevent: ne')).toEqual([
      { event: "delta", data: '{"text":"a"}' },
      { event: "done", data: "{}" },
    ]);
    expect(parser.push("xt\ndata: 1\n\n")).toEqual([{ event: "next", data: "1" }]);
  });

  it("handles a boundary falling inside the separator", () => {
    const parser = new EventStreamParser();
    expect(parser.push("event: a\ndata: 1\n")).toEqual([]);
    expect(parser.push("\n")).toEqual([{ event: "a", data: "1" }]);
  });

  it("ignores blocks without data", () => {
    expect(new EventStreamParser().push(": keep alive\n\n")).toEqual([]);
  });
});

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { everyWhileVisible, whenVisible } from "./visibility";

let hidden = false;

function setHidden(value: boolean) {
  hidden = value;
  document.dispatchEvent(new Event("visibilitychange"));
}

beforeEach(() => {
  vi.useFakeTimers();
  hidden = false;
  vi.spyOn(document, "hidden", "get").mockImplementation(() => hidden);
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("everyWhileVisible", () => {
  it("asks nothing while hidden and asks at once on coming back after a missed turn", () => {
    const fn = vi.fn();
    const stop = everyWhileVisible(fn, 1000);
    vi.advanceTimersByTime(1000);
    expect(fn).toHaveBeenCalledTimes(1);

    setHidden(true);
    vi.advanceTimersByTime(5000);
    expect(fn).toHaveBeenCalledTimes(1);

    setHidden(false);
    expect(fn).toHaveBeenCalledTimes(2);

    stop();
    vi.advanceTimersByTime(5000);
    expect(fn).toHaveBeenCalledTimes(2);
  });

  it("does not ask again when the page comes back before a turn is due", () => {
    const fn = vi.fn();
    const stop = everyWhileVisible(fn, 1000);
    setHidden(true);
    vi.advanceTimersByTime(400);
    setHidden(false);
    expect(fn).not.toHaveBeenCalled();
    stop();
  });
});

describe("whenVisible", () => {
  it("runs now when in view, otherwise once on coming back, unless cancelled", () => {
    const now = vi.fn();
    whenVisible(now);
    expect(now).toHaveBeenCalledTimes(1);

    setHidden(true);
    const later = vi.fn();
    whenVisible(later);
    expect(later).not.toHaveBeenCalled();
    setHidden(false);
    setHidden(true);
    setHidden(false);
    expect(later).toHaveBeenCalledTimes(1);

    setHidden(true);
    const cancelled = vi.fn();
    whenVisible(cancelled)();
    setHidden(false);
    expect(cancelled).not.toHaveBeenCalled();
  });
});

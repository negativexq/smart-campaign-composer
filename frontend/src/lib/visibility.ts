// A page in a hidden tab asks the API nothing. Coming back asks at once when a turn was
// missed, so what shows is never older than it would have been.

// everyWhileVisible calls fn every ms while the page is in view. It returns a stop.
export function everyWhileVisible(fn: () => unknown, ms: number): () => void {
  let last = Date.now();
  const run = () => {
    last = Date.now();
    void fn();
  };
  const timer = setInterval(() => {
    if (!document.hidden) run();
  }, ms);
  const onVisibility = () => {
    if (!document.hidden && Date.now() - last >= ms) run();
  };
  document.addEventListener("visibilitychange", onVisibility);
  return () => {
    clearInterval(timer);
    document.removeEventListener("visibilitychange", onVisibility);
  };
}

// whenVisible calls fn now, or when the page comes into view. It returns a cancel.
export function whenVisible(fn: () => unknown): () => void {
  if (!document.hidden) {
    void fn();
    return () => {};
  }
  const onVisibility = () => {
    if (document.hidden) return;
    document.removeEventListener("visibilitychange", onVisibility);
    void fn();
  };
  document.addEventListener("visibilitychange", onVisibility);
  return () => document.removeEventListener("visibilitychange", onVisibility);
}

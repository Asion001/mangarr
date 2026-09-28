import { afterEach, describe, expect, it, vi } from "vitest";

const hooks = vi.hoisted(() => ({ invalidateQueries: vi.fn(), cleanup: undefined as undefined | (() => void) }));
vi.mock("react", () => ({ useEffect: (effect: () => (() => void)) => { hooks.cleanup = effect(); }, useSyncExternalStore: vi.fn() }));
vi.mock("@tanstack/react-query", () => ({ useQueryClient: () => ({ invalidateQueries: hooks.invalidateQueries }) }));
vi.mock("../api/client", () => ({ basePath: "" }));
import { useLiveUpdates } from "./events";

class Stream {
  static current: Stream;
  listeners = new Map<string, (event: { data: string }) => void>();
  constructor() { Stream.current = this; }
  addEventListener(name: string, listener: (event: { data: string }) => void) { this.listeners.set(name, listener); }
  close() {}
  changed(name: string) { this.listeners.get("resource.changed")?.({ data: JSON.stringify({ payload: { name } }) }); }
}

afterEach(() => {
  hooks.cleanup?.();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

describe("task schedule live updates", () => {
  it("refreshes tasks and reader sync settings once for a burst of schedule changes", () => {
    vi.useFakeTimers();
    vi.stubGlobal("EventSource", Stream);
    vi.stubGlobal("navigator", { onLine: true });
    vi.stubGlobal("window", { setTimeout, addEventListener: vi.fn(), removeEventListener: vi.fn() });
    useLiveUpdates(true);
    Stream.current.changed("tasks");
    Stream.current.changed("tasks");
    vi.advanceTimersByTime(400);
    expect(hooks.invalidateQueries.mock.calls).toEqual([
      [{ queryKey: ["tasks"] }], [{ queryKey: ["settings", "readsync"] }],
    ]);
  });
});

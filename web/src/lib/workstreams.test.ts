import { Effect } from "effect";
import { afterEach, describe, expect, it, vi } from "vitest";
import { currentWorkstreams, loadWorkstreams } from "./workstreams";

describe("the read of the Workstream list", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("keeps the text of a request that fails as the error", async () => {
    vi.stubGlobal("fetch", () => Promise.reject(new TypeError("offline")));

    await Effect.runPromise(loadWorkstreams());

    const state = currentWorkstreams();
    expect(state.error).toBe("TypeError: offline");
    expect(state.list).toBeUndefined();
    expect(state.needsHuman).toEqual([]);
    expect(state.working.size).toBe(0);
  });

  it("keeps the text of the answer as the error", async () => {
    vi.stubGlobal(
      "fetch",
      async () => new Response(JSON.stringify({ error: "boom" }), { status: 500 }),
    );

    await Effect.runPromise(loadWorkstreams());

    expect(currentWorkstreams().error).toBe("boom");
  });
});

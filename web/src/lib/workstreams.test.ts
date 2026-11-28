import { Effect } from "effect";
import { afterEach, describe, expect, it, vi } from "vitest";
import { currentWorkstreams, loadWorkstreams } from "./workstreams";

describe("the read of the Workstream list", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("shows a clear text when the request fails", async () => {
    vi.stubGlobal("fetch", () => Promise.reject(new TypeError("offline")));

    await Effect.runPromise(loadWorkstreams());

    const state = currentWorkstreams();
    expect(state.error).toBe("No network connection.");
    expect(state.list).toBeUndefined();
    expect(state.needsHuman).toEqual([]);
    expect(state.working.size).toBe(0);
  });

  it("keeps the text of the answer as the error", async () => {
    vi.stubGlobal("fetch", async () => Response.json({ error: "boom" }, { status: 500 }));

    await Effect.runPromise(loadWorkstreams());

    expect(currentWorkstreams().error).toBe("boom");
  });

  it("shows the status when the answer is a page and not JSON", async () => {
    vi.stubGlobal("fetch", async () => new Response("<html>Bad Gateway</html>", { status: 502 }));

    await Effect.runPromise(loadWorkstreams());

    expect(currentWorkstreams().error).toBe("The server is not available (502).");
  });

  it("clears the error when a later request succeeds", async () => {
    vi.stubGlobal("fetch", async () => Response.json({ error: "boom" }, { status: 500 }));
    await Effect.runPromise(loadWorkstreams());
    expect(currentWorkstreams().error).toBe("boom");
    vi.stubGlobal("fetch", async (url: string) =>
      Response.json({ data: url === "/api/agents" ? { groups: [] } : [] }),
    );

    await Effect.runPromise(loadWorkstreams());

    expect(currentWorkstreams().error).toBeUndefined();
    expect(currentWorkstreams().list).toEqual([]);
  });
});

import { Effect } from "effect";
import { afterEach, describe, expect, it, vi } from "vitest";
import { request } from "@/lib/effects";
import { listWorkstreams } from "./api.gen";

describe("the fetch of the API client", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("gives the status and the error form for an HTML 502 answer", async () => {
    vi.stubGlobal(
      "fetch",
      async () =>
        new Response("<html>Bad Gateway</html>", {
          status: 502,
          headers: { "Content-Type": "text/html" },
        }),
    );

    const res = await listWorkstreams();

    expect(res.status).toBe(502);
    expect(res.data).toEqual({ error: "The server is not available (502)." });
  });

  it("gives the error of a JSON answer as the server sends it", async () => {
    vi.stubGlobal("fetch", async () => Response.json({ error: "boom" }, { status: 500 }));

    const res = await listWorkstreams();

    expect(res.status).toBe(500);
    expect(res.data).toEqual({ error: "boom" });
  });

  it("gives the data of a JSON answer and the headers", async () => {
    vi.stubGlobal("fetch", async () => Response.json({ data: [] }));

    const res = await listWorkstreams();

    expect(res.status).toBe(200);
    expect(res.data).toEqual({ data: [] });
    expect(res.headers.get("Content-Type")).toContain("json");
  });

  it("gives an empty value for an answer with no body", async () => {
    vi.stubGlobal("fetch", async () => new Response(null, { status: 204 }));

    const res = await listWorkstreams();

    expect(res.status).toBe(204);
    expect(res.data).toEqual({});
  });
});

const failure = (run: () => Promise<unknown>) =>
  Effect.runPromise(request(run).pipe(Effect.flip)).then((error) => error.message);

describe("the request effect", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("tells that there is no network when fetch fails", async () => {
    vi.stubGlobal("fetch", () => Promise.reject(new TypeError("Failed to fetch")));

    expect(await failure(() => listWorkstreams())).toBe("No network connection.");
  });

  it.each(["TimeoutError", "AbortError"])(
    "tells that the request did not finish for a %s",
    async (name) => {
      vi.stubGlobal("fetch", () => Promise.reject(new DOMException("stopped", name)));

      expect(await failure(() => listWorkstreams())).toBe("The request did not finish.");
    },
  );

  it("tells that the answer is not readable when the JSON does not parse", async () => {
    vi.stubGlobal(
      "fetch",
      async () => new Response("{", { headers: { "Content-Type": "application/json" } }),
    );

    expect(await failure(() => listWorkstreams())).toBe(
      "The server sent an answer that Mobius cannot read.",
    );
  });

  it("tells that the answer is not readable when a good status has a page", async () => {
    vi.stubGlobal("fetch", async () => new Response("<html></html>"));

    expect(await failure(() => listWorkstreams())).toBe(
      "The server sent an answer that Mobius cannot read.",
    );
  });
});

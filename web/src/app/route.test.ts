import { Option } from "effect";
import { describe, expect, it } from "vitest";
import { fromString } from "foldkit/url";
import { AppRoute, parseRoute, routeToPath } from "./route";

const parsed = (path: string) => {
  const url = fromString(`http://localhost:6363${path}`);
  if (Option.isNone(url)) {
    throw new Error(`Invalid URL: ${path}`);
  }
  return parseRoute(url.value);
};

describe("the search of a route", () => {
  it("reads a list of the Usage search as JSON", () => {
    const route = parsed("/usage?group=%5B%22model%22%5D&period=7d&model=%5B%22opus%22%5D");
    expect(route._tag).toBe("Usage");
    if (route._tag === "Usage") {
      expect(route.search.group).toEqual(["model"]);
      expect(route.search.model).toEqual(["opus"]);
    }
  });

  it("writes the Usage search as the address of earlier releases", () => {
    const route = AppRoute.Usage({ search: { tab: "tokens", group: ["model", "effort"] } });
    expect(routeToPath(route)).toBe("/usage?tab=tokens&group=%5B%22model%22%2C%22effort%22%5D");
  });

  it("reads the number of the MCP servers search", () => {
    const route = parsed("/settings/mcp-servers?server=3&oauth=connected");
    expect(route).toEqual(
      AppRoute.McpServers({ search: { server: 3, oauth: "connected", reason: undefined } }),
    );
  });
});

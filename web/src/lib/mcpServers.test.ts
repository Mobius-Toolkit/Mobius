import { describe, expect, it } from "vitest";
import { validateMcpServersSearch } from "./mcpServers";

describe("the search of the MCP servers page", () => {
  it("gives no field for a missing search", () => {
    expect(validateMcpServersSearch({})).toEqual({});
  });

  it("keeps each valid value", () => {
    expect(
      validateMcpServersSearch({ oauth: "connected", server: 3, reason: "access_denied" }),
    ).toEqual({ oauth: "connected", server: 3, reason: "access_denied" });
    expect(validateMcpServersSearch({ oauth: "failed" }).oauth).toBe("failed");
  });

  it("reads the number of a server that the address holds as text", () => {
    expect(validateMcpServersSearch({ server: "7" }).server).toBe(7);
  });

  it("drops a value that is not valid and keeps the other fields", () => {
    expect(validateMcpServersSearch({ oauth: "pending", server: 0, reason: 5 })).toEqual({});
    expect(validateMcpServersSearch({ server: -2, reason: "denied" })).toEqual({
      reason: "denied",
    });
    expect(validateMcpServersSearch({ server: 1.5 }).server).toBeUndefined();
    expect(validateMcpServersSearch({ server: "abc" }).server).toBeUndefined();
    expect(validateMcpServersSearch({ server: null }).server).toBeUndefined();
  });
});

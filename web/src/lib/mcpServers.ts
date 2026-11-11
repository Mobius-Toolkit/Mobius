export type McpServersSearch = {
  oauth?: "connected" | "failed";
  server?: number;
  reason?: string;
};

// The callback of an MCP OAuth flow sends the browser to the page with the result in the query.
export function validateMcpServersSearch(search: Record<string, unknown>): McpServersSearch {
  const server = Number(search.server);
  return {
    oauth: search.oauth === "connected" || search.oauth === "failed" ? search.oauth : undefined,
    server: Number.isInteger(server) && server > 0 ? server : undefined,
    reason: typeof search.reason === "string" ? search.reason : undefined,
  };
}

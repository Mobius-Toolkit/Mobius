import { expect, type Page } from "@playwright/test";

// The API request of the page carries the device login, but the request context of Playwright does not.
export const addMcpServer = (
  page: Page,
  organization: string,
  body: { name: string; kind: "http" | "stdio"; url?: string; command?: string },
) =>
  page.evaluate(
    async (call) => {
      const response = await fetch(`/api/organizations/${call.organization}/mcp-servers`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(call.body),
      });
      if (response.status !== 201) {
        throw new Error(`POST mcp-servers answered ${response.status}`);
      }
      const { data } = (await response.json()) as { data: { id: number } };
      return data.id;
    },
    { organization, body },
  );

export const selectMcpServers = async (page: Page, workstream: string, ids: number[]) => {
  const status = await page.evaluate(
    (call) =>
      fetch(`/api/workstreams/${call.workstream}/mcp-servers`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ids: call.ids }),
      }).then((response) => response.status),
    { workstream, ids },
  );
  expect(status).toBe(204);
};

export const deleteMcpServers = async (page: Page, organization: string, ids: number[]) => {
  for (const id of ids) {
    const status = await page.evaluate(
      (call) =>
        fetch(`/api/organizations/${call.organization}/mcp-servers/${call.id}`, {
          method: "DELETE",
        }).then((response) => response.status),
      { organization, id },
    );
    expect(status).toBe(204);
  }
};

import type { Page } from "@playwright/test";

// Adds the stopped Inbox items of the issues of a Workstream of plants/garden to the server. The poll dismisses an
// item when its issue loses mobius:needs-human.
export async function addStoppedItems(page: Page, workstream: number, issues: [number, string][]) {
  const ids: number[] = [];
  for (const [issue, title] of issues) {
    const query = new URLSearchParams({
      kind: "stopped",
      repository: "plants/garden",
      workstream: String(workstream),
      issue: String(issue),
      title,
      reason: "The agent stopped",
    });
    const response = await page.request.post(`/e2e/inbox-items?${query}`);
    ids.push(Number(await response.text()));
  }
  return ids;
}

export async function removeItems(page: Page, ids: number[]) {
  for (const id of ids) {
    await page.request.delete(`/e2e/inbox-items/${id}`);
  }
}

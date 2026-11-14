import type { Locator, Page } from "@playwright/test";

// Selects the first text of the element that holds the words, as the Owner does with a pointer.
export async function selectText(element: Locator, words: string) {
  await element.evaluate((root, needle) => {
    const document = root.ownerDocument;
    const walker = document.createTreeWalker(root, 4);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      const start = (node.textContent ?? "").indexOf(needle);
      if (start >= 0) {
        const range = document.createRange();
        range.setStart(node, start);
        range.setEnd(node, start + needle.length);
        const selection = document.defaultView?.getSelection();
        selection?.removeAllRanges();
        selection?.addRange(range);
        return;
      }
    }
    throw new Error(`The element has no text "${needle}".`);
  }, words);
}

export const selectionBottom = (page: Page) =>
  page.evaluate<number>("[...getSelection().getRangeAt(0).getClientRects()].at(-1).bottom");

export const selectionIsEmpty = (page: Page) =>
  page.evaluate<boolean>("getSelection().isCollapsed");

// The Brief tab shows the brief that the list of Workstreams gives.
export const routeBrief = (page: Page, repository: string, number: number, brief: string) =>
  page.route("**/api/workstreams", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as {
      data: { repository: string; number: number; brief: string }[];
    };
    for (const workstream of body.data) {
      if (workstream.repository === repository && workstream.number === number) {
        workstream.brief = brief;
      }
    }
    await route.fulfill({ response, json: body });
  });

// A phone shows the Brief in its own sheet. A desktop shows it in the tab Brief of the side panel.
export const briefOf = (page: Page) =>
  page
    .getByRole("tabpanel", { name: "Brief" })
    .or(page.getByRole("dialog", { name: "Brief" }))
    .filter({ visible: true });

export async function openBriefTab(page: Page, device: string) {
  if (device === "phone") {
    await page.getByRole("banner").getByRole("button", { name: "Brief", exact: true }).click();
  } else {
    await page.getByRole("tab", { name: "Brief" }).click();
  }
  return briefOf(page);
}

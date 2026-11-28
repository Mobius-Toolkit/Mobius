import { expect, type Page } from "@playwright/test";
import { png } from "./images.js";

export const planItem = {
  id: 3,
  reference: "#41",
  title: "Add plan model",
  reason: "Mobius lost the task",
  link: "https://github.com/owner/shop/issues/41",
};

// The selection as the server gets it. The draft adds an id.
export const quotedPart = {
  source: "message",
  text: "works on #41",
  context: "The Implementer works on #41.",
  reference: "",
  link: "",
  part: "",
  messageId: 5,
  messageAuthor: "Lead",
};

// The tests have no DOM types, so the scripts read and write the draft store in the browser. The page must have opened
// the store before, because the store of a page that has not used a draft has no table.
const inStore = (mode: "readonly" | "readwrite", use: string) => `new Promise((resolve, reject) => {
  const open = indexedDB.open("mobius");
  open.onsuccess = () => {
    const table = open.result.transaction("drafts", "${mode}").objectStore("drafts");
    const request = ${use};
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  };
})`;

async function storeDraft(page: Page, key: string) {
  const draft = {
    key,
    text: "",
    images: [],
    inboxItems: [planItem],
    selections: [{ id: "quote-1", ...quotedPart }],
    voice: null,
  };
  await page.evaluate(inStore("readwrite", `table.put(${JSON.stringify(draft)})`));
}

export const storedImages = (page: Page, key: string) =>
  page.evaluate<number>(`${inStore("readonly", `table.get(${JSON.stringify(key)})`)}
    .then((draft) => (draft ? draft.images.length : 0))`);

// The key of the draft of the answer panel of the first Inbox item that the server holds and that matches.
export async function answerDraftKeyOf(
  page: Page,
  matches: (item: { kind: string; action: { tool: string; state: string } | null }) => boolean,
) {
  const response = await page.request.get("/api/inbox");
  const body = (await response.json()) as {
    data: { id: number; kind: string; action: { tool: string; state: string } | null }[];
  };
  const found = body.data.find(matches);
  expect(found).toBeDefined();
  return `answerDraft:${found?.id}`;
}

// Gives the answer panel that open() shows the draft with an Inbox item, a selection and an image, as the Chat button, the
// quote popover and the image button give them to the field of the chat. The page loads again, because it keeps no draft
// of an earlier read.
export async function attachToPanel(page: Page, key: string, open: () => Promise<void>) {
  const panel = page.getByRole("dialog");
  const field = panel.locator("textarea");
  await open();
  await expect(field).toBeEditable();
  await page.keyboard.press("Escape");
  await expect(panel).toBeHidden();
  await storeDraft(page, key);
  await page.reload();
  await open();
  await expect(field).toBeEditable();
  await expect(panel.locator("[data-attachment-card='inbox-item']")).toContainText(planItem.title);
  await expect(panel.locator("[data-attachment-card='selection']")).toContainText(quotedPart.text);
  await panel.locator("input[type=file]").setInputFiles({
    name: "plan.png",
    mimeType: "image/png",
    buffer: await png(page, 40, 30, "#2563eb"),
  });
  await expect(panel.getByRole("img", { name: "Image 1" })).toBeVisible();
  await expect.poll(() => storedImages(page, key)).toBe(1);
  return panel;
}

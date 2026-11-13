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

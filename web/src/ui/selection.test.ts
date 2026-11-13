// @vitest-environment happy-dom
import { beforeEach, describe, expect, it } from "vitest";
import { selectionContext } from "./selection";

function rangeOf(from: Node, fromOffset: number, to: Node, toOffset: number) {
  const range = document.createRange();
  range.setStart(from, fromOffset);
  range.setEnd(to, toOffset);
  return range;
}

function textOf(selector: string) {
  const node = document.querySelector(selector)?.firstChild;
  if (!node) {
    throw new Error(`No text in ${selector}`);
  }
  return node;
}

describe("selectionContext", () => {
  let root: Element;

  beforeEach(() => {
    document.body.innerHTML = `<div id="root">
      <p id="first">The first paragraph has a long text.</p>
      <ul><li id="item">One list item</li><li id="other">Another list item</li></ul>
      <blockquote><p id="quoted">A quoted paragraph.</p></blockquote>
      <p id="last">The last paragraph.</p>
    </div>`;
    const found = document.getElementById("root");
    if (!found) {
      throw new Error("No root");
    }
    root = found;
  });

  it("gives the paragraph of a selection inside one paragraph", () => {
    const text = textOf("#first");
    expect(selectionContext(root, rangeOf(text, 4, text, 9))).toBe(
      "The first paragraph has a long text.",
    );
  });

  it("gives the list item of a selection inside one list item", () => {
    const text = textOf("#item");
    expect(selectionContext(root, rangeOf(text, 4, text, 8))).toBe("One list item");
  });

  it("gives each block from the start to the end, with an empty line between two blocks", () => {
    expect(selectionContext(root, rangeOf(textOf("#first"), 4, textOf("#other"), 7))).toBe(
      "The first paragraph has a long text.\n\nOne list item\n\nAnother list item",
    );
  });

  it("gives the paragraph inside a quote one time", () => {
    expect(selectionContext(root, rangeOf(textOf("#other"), 0, textOf("#last"), 3))).toBe(
      "Another list item\n\nA quoted paragraph.\n\nThe last paragraph.",
    );
  });

  it("leaves out the block where the selection ends at its start", () => {
    const last = textOf("#last");
    expect(selectionContext(root, rangeOf(textOf("#first"), 0, last, 0))).toBe(
      "The first paragraph has a long text.\n\nOne list item\n\nAnother list item\n\nA quoted paragraph.",
    );
  });

  it("keeps the block where the selection ends at the start of a text after an inline element", () => {
    document.body.innerHTML = `<div id="root">
      <p id="before">First paragraph.</p>
      <p id="mixed">See <strong>this</strong><span id="rest"> rest.</span></p>
    </div>`;
    const mixed = document.getElementById("root");
    if (!mixed) {
      throw new Error("No root");
    }
    expect(selectionContext(mixed, rangeOf(textOf("#before"), 6, textOf("#rest"), 0))).toBe(
      "First paragraph.\n\nSee this rest.",
    );
  });
});

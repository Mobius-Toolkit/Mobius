// @vitest-environment happy-dom
import { beforeEach, describe, expect, it } from "vitest";
import { selectionContext, selectionPart } from "./selection";

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

describe("selectionPart", () => {
  let root: Element;

  beforeEach(() => {
    document.body.innerHTML = `<div id="root">
      <p id="intro">Text above the first heading.</p>
      <h2 id="goal-heading">Goal</h2>
      <p id="goal">Let the Owner ask.</p>
      <p><strong id="scope-heading">Scope</strong></p>
      <ul><li id="scope">Add a popover.</li></ul>
      <p id="limits"><strong>Limits</strong>\nDo not add a button.</p>
      <p id="note"><strong>Note</strong> this line has more text.</p>
    </div>`;
    const found = document.getElementById("root");
    if (!found) {
      throw new Error("No root");
    }
    root = found;
  });

  const partOf = (selector: string) => {
    const text = textOf(selector);
    return selectionPart(root, rangeOf(text, 0, text, 1));
  };

  it("is empty above the first heading", () => {
    expect(partOf("#intro")).toBe("");
  });

  it("gives the heading element above the selection", () => {
    expect(partOf("#goal")).toBe("Goal");
  });

  it("gives the paragraph of only bold text above the selection", () => {
    expect(partOf("#scope")).toBe("Scope");
  });

  it("gives the paragraph that starts with a line of only bold text", () => {
    expect(partOf("#limits")).toBe("Limits");
  });

  it("does not take a paragraph with bold text at the start of a line of other text", () => {
    expect(partOf("#note")).toBe("Limits");
  });

  it("gives the heading that holds the start of the selection", () => {
    expect(partOf("#goal-heading")).toBe("Goal");
    expect(partOf("#scope-heading")).toBe("Scope");
  });

  it("gives the section of the start of a selection that spans more than one section", () => {
    expect(selectionPart(root, rangeOf(textOf("#goal"), 0, textOf("#scope"), 3))).toBe("Goal");
  });
});

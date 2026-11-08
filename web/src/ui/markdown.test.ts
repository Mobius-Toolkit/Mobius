import { inertHtml } from "foldkit/html";
import { describe, expect, it } from "vitest";
import { briefMarkdown } from "@/lib/briefDiff";
import { diffLines } from "@/lib/diff";
import { beginMarkdownRender, parseMarkdown, renderMarkdown, shieldEquals } from "./markdown";

type Vnode = { sel?: string; text?: string; children?: Array<Vnode | string | null> };

// The text of each node that has an ins element above it.
function insertedTexts(text: string) {
  const found: string[] = [];
  const walk = (node: Vnode | string | null, inside: boolean) => {
    if (node === null || typeof node === "string") {
      return;
    }
    const open = inside || node.sel === "ins";
    if (node.text !== undefined && open) {
      found.push(node.text);
    }
    node.children?.forEach((child) => walk(child, open));
  };
  walk(renderMarkdown(inertHtml, text, true), false);
  return found;
}

const render = (text: string, diff = false) =>
  JSON.stringify(renderMarkdown(inertHtml, text, diff));

describe("renderMarkdown", () => {
  it("shows raw HTML as text", () => {
    const result = render("<div>x</div> and <b>y</b>");
    expect(result).toContain("<b>y</b>");
    expect(result).not.toContain('"sel":"b"');
  });

  it("shows a block of raw HTML as text", () => {
    const result = render("<div>x</div>\n\nafter");
    expect(result).toContain('"text":"<div>x</div>"');
    expect(result).toContain("after");
  });

  it("resolves a link definition, and shows a reference with no definition as it is", () => {
    const result = render("[note]: https://example.com\n\nSee [the note][note] and [x][missing].");
    expect(result).toContain('"href":"https://example.com"');
    expect(result).toContain("the note");
    expect(result).toContain("[x][missing]");
    expect(result).not.toContain("[note]:");
  });

  it("shows a footnote with its reference", () => {
    const result = render("Text[^1] here\n\n[^1]: footnote text");
    expect(result).toContain('"href":"#1"');
    expect(result).toContain("footnote text");
    expect(result).toContain('"sel":"footer"');
  });

  it("shows a --- block at the start as text", () => {
    const result = render("---\ntitle: a\n---\n\nbody");
    expect(result).toContain("title: a");
    expect(result).toContain("body");
  });

  it("keeps a colon between two words and a time", () => {
    const result = render("Keep a:b and 09:30 UTC, key:value.");
    expect(result).toContain("a:b and 09:30 UTC, key:value.");
  });

  it("keeps the URL of a link with an unknown scheme", () => {
    const result = render("[open](foo://bar/baz?x=1)");
    expect(result).toContain('"href":"foo://bar/baz?x=1"');
  });

  it("gives the link of a text with a safe scheme to the element", () => {
    expect(render("[open](https://example.com/a)")).toContain('"href":"https://example.com/a"');
    expect(render("[mail](mailto:a@example.com)")).toContain('"href":"mailto:a@example.com"');
  });

  it("drops the URL of a javascript:, vbscript: and data: link and image, and keeps their text", () => {
    for (const url of [
      "javascript:alert(1)",
      "JavaScript:alert(1)",
      "vbscript:msgbox(1)",
      "data:text/html,x",
    ]) {
      const result = render(`[open](${url}) ![pic](${url})`);
      expect(result).not.toContain('"href"');
      expect(result).toContain("open");
      expect(result).toContain("pic");
    }
  });

  it("drops the URL of a link with a script scheme and a tab or a new line inside", () => {
    expect(render("[open](<java\tscript:alert(1)>)")).not.toContain('"href"');
  });

  it("shows an image as a link", () => {
    const result = render("![alt text](https://example.com/a.png)");
    expect(result).toContain('"href":"https://example.com/a.png"');
    expect(result).toContain("alt text");
    expect(result).not.toContain('"sel":"img"');
  });

  it("renders the nodes of GitHub Markdown: a table, a task list, a strike", () => {
    const result = render("- [x] done\n- [ ] todo\n\n| a | b |\n|---|:-:|\n| 1 | 2 |\n\n~~gone~~");
    expect(result).toContain('"sel":"table"');
    expect(result).toContain('"sel":"input"');
    expect(result).toContain('"sel":"del"');
    expect(result).toContain('"text-align":"center"');
  });

  it("keeps == and === of a chat message as text", () => {
    const result = render("if x == 1 then, a === b");
    expect(result).toContain("if x == 1 then, a === b");
    expect(result).not.toContain("bg-green-500/40");
  });

  it("does not mark ~~text~~ of a chat message as a removed line", () => {
    expect(render("a ~~b~~ c")).not.toContain("bg-red-500/40");
  });

  it("marks the added text of a Brief diff, and keeps the == of an unchanged line", () => {
    const before = "Check x == 1 first\nKeep this";
    const after = "Check x == 1 first\nKeep this now";
    const marked = briefMarkdown(diffLines(shieldEquals(before), shieldEquals(after)));
    const result = render(marked, true);
    expect(result).toContain("bg-green-500/40");
    expect(result).toContain("Check x == 1 first");
  });

  it("keeps an added text open across the inline nodes of its block", () => {
    const shown = insertedTexts(
      "- ==Use `make fmt` and **bold** and [a link](https://example.com) before each commit==, then plain",
    );
    expect(shown).toEqual([
      "Use ",
      "make fmt",
      " and ",
      "bold",
      " and ",
      "a link",
      " before each commit",
    ]);
  });

  it("closes an added text at its second mark, and opens the next one", () => {
    expect(insertedTexts("==one== plain **bold** ==two `code`==")).toEqual(["one", "two ", "code"]);
  });

  it("starts each block with no open added text", () => {
    expect(insertedTexts("==open\n\nnext block")).toEqual(["open"]);
  });

  it("gives an empty view for an empty text", () => {
    expect(render("")).not.toContain("markdown");
  });
});

describe("parseMarkdown", () => {
  it("keeps the result of each text of a chat that is longer than 500 texts, from one render to the next", () => {
    const texts = Array.from({ length: 700 }, (_, index) => `Message ${index} with **bold** text.`);
    beginMarkdownRender();
    const first = texts.map((text) => parseMarkdown(text));
    beginMarkdownRender();
    const second = texts.map((text) => parseMarkdown(text));
    expect(second.every((document, index) => document === first[index])).toBe(true);
  });

  it("drops a text that the last render did not use", () => {
    beginMarkdownRender();
    const first = parseMarkdown("A text that leaves the screen.");
    beginMarkdownRender();
    beginMarkdownRender();
    expect(parseMarkdown("A text that leaves the screen.")).not.toBe(first);
  });
});

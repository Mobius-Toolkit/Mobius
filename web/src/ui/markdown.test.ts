import { Result } from "effect";
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

const plainClass = '"whitespace-pre-line":true,"break-words":true';

describe("renderMarkdown", () => {
  it("renders simple Markdown", () => {
    const result = render(
      "# Title\n\n- one\n- two\n\n**bold** `code` [a link](https://example.com)\n\n```\nblock\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |",
    );
    for (const element of ["h1", "ul", "li", "strong", "code", "a", "pre", "table"]) {
      expect(result).toContain(`"sel":"${element}"`);
    }
    expect(result).not.toContain(plainClass);
  });

  it("shows raw HTML as plain text", () => {
    const result = render("<div>x</div> and <b>y</b>");
    expect(result).toContain(plainClass);
    expect(result).toContain("<b>y</b>");
    expect(result).not.toContain('"sel":"b"');
  });

  it("shows a block of raw HTML as plain text", () => {
    const result = render("<div>x</div>\n\nafter");
    expect(result).toContain(plainClass);
    expect(result).toContain("<div>x</div>");
    expect(result).toContain("after");
  });

  it("shows a link definition as plain text", () => {
    const result = render("[note]: https://example.com\n\nSee [the note][note].");
    expect(result).toContain(plainClass);
    expect(result).toContain("[note]: https://example.com");
    expect(result).not.toContain('"href"');
  });

  it("shows a footnote as plain text", () => {
    const result = render("Text[^1] here\n\n[^1]: footnote text");
    expect(result).toContain(plainClass);
    expect(result).toContain("footnote text");
    expect(result).not.toContain('"sel":"footer"');
  });

  it("shows a --- block at the start as plain text", () => {
    const result = render("---\ntitle: a\n---\n\nbody");
    expect(result).toContain(plainClass);
    expect(result).toContain("title: a");
    expect(result).toContain("body");
  });

  it("shows the plain text with the local times", () => {
    expect(render("<b>Keep 2026-11-12 09:30 UTC</b>")).not.toContain("UTC");
  });

  it("keeps the Markdown of a text with a time and a colon between two words", () => {
    const result = render("**Keep** a:b and 09:30 UTC, key:value, mobius:ready, see :ready.");
    expect(result).not.toContain(plainClass);
    expect(result).toContain('"sel":"strong"');
    expect(result).toContain("a:b and 09:30 UTC, key:value, mobius:ready, see :ready.");
    expect(result).not.toContain("\\\\");
  });

  it("keeps a colon of a leaf directive line as text", () => {
    const result = render("::note\n\nafter");
    expect(result).not.toContain(plainClass);
    expect(result).toContain("::note");
  });

  it("keeps a colon in inline code and in a code block with no backslash", () => {
    const result = render(
      "Run `mobius:ready` and ``a:b ` c:d`` now, at 09:30.\n\n```sh\nlabel mobius:ready\n```\n\n~~~\nx:y\n~~~\n\nend:of",
    );
    expect(result).not.toContain(plainClass);
    expect(result).toContain('"text":"mobius:ready"');
    expect(result).toContain('"text":"a:b ` c:d"');
    expect(result).toContain('"text":"label mobius:ready"');
    expect(result).toContain('"text":"x:y"');
    expect(result).toContain("end:of");
    expect(result).not.toContain("\\\\");
  });

  it("keeps a colon and a [x] in an indented code block as they are", () => {
    const result = render(
      "Run:\n\n    label mobius:ready\n\n    - [x] a\n\tkey:value\n\nafter:this and **bold** 09:30",
    );
    expect(result).not.toContain(plainClass);
    expect(result).toContain('"text":"label mobius:ready\\n\\n- [x] a\\nkey:value"');
    expect(result).toContain("after:this and ");
    expect(result).toContain('"sel":"strong"');
    expect(result).not.toContain("\\\\");
    expect(result).not.toContain('"sel":"input"');
  });

  it("keeps a colon in an indented code block at the start of a text", () => {
    const result = render("    mobius:ready");
    expect(result).toContain('"text":"mobius:ready"');
    expect(result).not.toContain("\\\\");
  });

  it("keeps a colon in a code block that has no closing fence, and the text before it", () => {
    const result = render("a:b\n\n```\nx:y");
    expect(result).not.toContain(plainClass);
    expect(result).toContain('"text":"a:b"');
    expect(result).toContain('"text":"x:y"');
  });

  it("keeps a colon of the URL of a link", () => {
    const result = render("[open](https://example.com/a:b) and https://example.com/c:d");
    expect(result).not.toContain(plainClass);
    expect(result).toContain('"href":"https://example.com/a:b"');
    expect(result).toContain('"href":"https://example.com/c:d"');
  });

  it("shows a link with an unknown scheme as plain text", () => {
    const result = render("[open](foo://bar/baz?x=1)");
    expect(result).toContain(plainClass);
    expect(result).not.toContain('"href"');
  });

  it("gives the link of a text with a safe scheme to the element", () => {
    expect(render("[open](https://example.com/a)")).toContain('"href":"https://example.com/a"');
    expect(render("[mail](mailto:a@example.com)")).toContain('"href":"mailto:a@example.com"');
  });

  it("opens each link in a new tab", () => {
    const result = render("[open](https://example.com/a)");
    expect(result).toContain('"target":"_blank"');
    expect(result).toContain('"rel":"noopener noreferrer"');
  });

  it("shows a javascript:, vbscript: and data: link and image as plain text with no link", () => {
    for (const url of [
      "javascript:alert(1)",
      "JavaScript:alert(1)",
      "vbscript:msgbox(1)",
      "data:text/html,x",
    ]) {
      const result = render(`[open](${url}) ![pic](${url})`);
      expect(result).toContain(plainClass);
      expect(result).not.toContain('"href"');
      expect(result).not.toContain('"sel":"a"');
      expect(result).toContain("open");
      expect(result).toContain("pic");
    }
  });

  it("shows a link with a script scheme and a tab inside as plain text with no link", () => {
    const result = render("[open](<java\tscript:alert(1)>)");
    expect(result).toContain(plainClass);
    expect(result).not.toContain('"href"');
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

  it("shows a task list with a checkbox for each item, and keeps a [x] that is no task item", () => {
    const result = render("- [x] done **now**\n- [ ] todo\n\nSee [x] and `- [ ] code`.");
    expect(result).not.toContain(plainClass);
    expect(result.match(/"sel":"input"/g)).toHaveLength(2);
    expect(result.match(/"checked":true/g)).toHaveLength(1);
    expect(result).toContain("done ");
    expect(result).toContain('"sel":"strong"');
    expect(result).toContain("See [x] and ");
    expect(result).toContain('"text":"- [ ] code"');
  });

  it("keeps == and === of a chat message as text", () => {
    const result = render("if x == 1 then, a === b");
    expect(result).toContain("if x == 1 then, a === b");
    expect(result).not.toContain("bg-green-500/40");
  });

  it("does not mark ~~text~~ of a chat message as a removed line", () => {
    expect(render("a ~~b~~ c")).not.toContain("bg-red-500/40");
  });

  it("keeps the == of a Brief diff that shows as plain text", () => {
    const before = "Check x == 1 first<br>\nKeep this";
    const after = "Check x == 1 first<br>\nKeep this now";
    const marked = briefMarkdown(diffLines(shieldEquals(before), shieldEquals(after)));
    const result = render(marked, true);
    expect(result).toContain(plainClass);
    expect(result).toContain("Check x == 1 first<br>");
    expect(result).not.toContain("");
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

  it("keeps the failed result of a text that the package cannot parse", () => {
    beginMarkdownRender();
    const first = parseMarkdown("<b>not Markdown</b>");
    expect(Result.isFailure(first)).toBe(true);
    beginMarkdownRender();
    expect(parseMarkdown("<b>not Markdown</b>")).toBe(first);
  });

  it("drops a text that the last render did not use", () => {
    beginMarkdownRender();
    const first = parseMarkdown("A text that leaves the screen.");
    beginMarkdownRender();
    beginMarkdownRender();
    expect(parseMarkdown("A text that leaves the screen.")).not.toBe(first);
  });
});

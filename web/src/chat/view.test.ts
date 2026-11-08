import { describe, expect, it } from "vitest";
import { inertHtml } from "foldkit/html";
import { briefMarkdown } from "@/lib/briefDiff";
import { diffLines } from "@/lib/diff";
import { shieldEquals } from "./markdown";
import { renderMarkdown } from "./view";

const render = (text: string, diff = false) =>
  JSON.stringify(renderMarkdown(inertHtml, text, diff));

describe("renderMarkdown", () => {
  it("keeps == and === of a chat message as text", () => {
    const result = render("if x == 1 then, a === b");
    expect(result).toContain("if x == 1 then, a === b");
    expect(result).not.toContain("bg-green-500/40");
  });

  it("does not mark ~~text~~ of a chat message as a removed line", () => {
    expect(render("a ~~b~~ c")).not.toContain("bg-red-500/40");
  });

  it("shows a javascript: link and a javascript: image as text, with no link", () => {
    for (const text of ["[open](javascript:alert(1))", "![pic](javascript:alert(2))"]) {
      const result = render(text);
      expect(result).not.toContain('"href"');
      expect(result).toContain(text);
    }
  });

  it("gives the link of a text with a safe scheme to the element", () => {
    expect(render("[open](https://example.com/a)")).toContain('"href":"https://example.com/a"');
  });

  it("marks the added text of a Brief diff, and keeps the == of an unchanged line", () => {
    const before = "Check x == 1 first\nKeep this";
    const after = "Check x == 1 first\nKeep this now";
    const marked = briefMarkdown(diffLines(shieldEquals(before), shieldEquals(after)));
    const result = render(marked, true);
    expect(result).toContain("bg-green-500/40");
    expect(result).toContain("Check x == 1 first");
    expect(result).not.toContain("");
  });
});

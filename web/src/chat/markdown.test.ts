import { describe, expect, it } from "vitest";
import { beginMarkdownRender, parseChatMarkdown } from "./markdown";

const json = (text: string) => JSON.stringify(parseChatMarkdown(text));

describe("parseChatMarkdown", () => {
  it("parses a text with a time", () => {
    expect(parseChatMarkdown("Keep 09:30 UTC.")).toBeDefined();
  });

  it("keeps a colon in code and in a URL as it is", () => {
    const text = "Done at 09:30, see `key:value` and http://localhost:6363/chat.";
    const result = json(text);
    expect(result).toContain("key:value");
    expect(result).not.toContain("key\\\\:value");
    expect(result).toContain("http://localhost:6363/chat");
    expect(result).not.toContain("localhost\\\\:6363");
  });

  it("keeps the Markdown of a text with raw HTML outside code", () => {
    const result = json("Use **bold** for Foo<Bar> at 09:30.");
    expect(result).toContain("Strong");
  });

  it("gives no document for a text that fails both parses, and keeps that answer", () => {
    const text =
      "[note]: https://example.com\n\n---\ntitle: a\n---\n\n<div>x</div>\n\n[^1]: footnote";
    const first = parseChatMarkdown(text);
    expect(parseChatMarkdown(text)).toBe(first);
  });

  it("keeps the result of each text of a chat that is longer than 500 texts, from one render to the next", () => {
    const texts = Array.from({ length: 700 }, (_, index) => `Message ${index} with **bold** text.`);
    beginMarkdownRender();
    const first = texts.map((text) => parseChatMarkdown(text));
    beginMarkdownRender();
    const second = texts.map((text) => parseChatMarkdown(text));
    expect(second.every((document, index) => document === first[index])).toBe(true);
  });

  it("drops a text that the last render did not use", () => {
    beginMarkdownRender();
    const first = parseChatMarkdown("A text that leaves the screen.");
    beginMarkdownRender();
    beginMarkdownRender();
    expect(parseChatMarkdown("A text that leaves the screen.")).not.toBe(first);
  });
});

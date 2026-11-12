import { defaultViews, viewBlocks } from "@foldkit/markdown";
import type { MarkdownDocument, Views } from "@foldkit/markdown";
import { parseMarkdown as parseDocument } from "@foldkit/markdown/vite";
import { Result } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { MarkdownFailed } from "@/lib/effects";
import { localTimes } from "@/lib/time";

type Parsed = Result.Result<MarkdownDocument, MarkdownFailed>;
type Entry = { parsed: Parsed; render: number };

const parsedTexts = new Map<string, Entry>();
const renders = { count: 0 };

// The view renders again after each Message, so the result of the parse of a text is kept, also when the parse failed.
// Each render calls beginMarkdownRender first. A text that the last render did not use leaves the cache, so the cache
// holds the texts of the chat on the screen, however long the chat is.
export function beginMarkdownRender() {
  renders.count++;
  for (const [text, entry] of parsedTexts) {
    if (entry.render < renders.count - 1) {
      parsedTexts.delete(text);
    }
  }
}

// briefMarkdown puts "==" around an added text. A "==" in the text of the Brief is shielded with a private-use
// character before the diff, so it cannot open an added text, and the views of the Brief diff restore it.
const shieldedEquals = "";

export function shieldEquals(text: string) {
  return text.replaceAll("==", shieldedEquals.repeat(2));
}

function restoreEquals(text: string) {
  return text.replaceAll(shieldedEquals, "=");
}

// The parser of @foldkit/markdown reads ":name" as a text directive and throws on a task list item. Outside code, a
// colon before a letter or a digit gets a backslash, and the "[ ]" or "[x]" of a task list item becomes a private-use
// character that the Text view shows as a checkbox. A URL keeps its colons, because a backslash breaks a link that has
// no brackets. The groups of the pattern are a code fence, a code span and the letter of a task list item. An indented code block
// is a block of lines with four spaces or a tab, after a blank line or at the start.
const checkedBox = "";
const uncheckedBox = "";
const outsideCode =
  /^[ \t>-]*(`{3,}(?![^\n]*`)|~{3,})[^\n]*(?:\n[\s\S]*?)?(?:\n[ \t>-]*\1[`~]*[ \t]*(?=\n|$)|(?![\s\S]))|(?<!`)(`+)(?!`)[\s\S]*?(?<!`)\2(?!`)|(?:(?<![\s\S])|(?<=\n[ \t]*\n))(?: {4}|\t)[^\n]*(?:\n(?:[ \t]*(?=\n)|(?: {4}|\t)[^\n]*))*|^[ \t>]*(?:[-*+]|\d+[.)])[ \t]+\[([ xX])\](?=[ \t])|\b(?:(?:https?|ftp):\/\/|www\.|mailto:)[^\s<]*|(?<!\\):(?=[A-Za-z0-9])/gim;

function prepare(text: string) {
  return text.replace(
    outsideCode,
    (match, _fence: string | undefined, _span: string | undefined, box: string | undefined) => {
      if (box !== undefined) {
        return match.slice(0, -3) + (box === " " ? uncheckedBox : checkedBox);
      }
      return match === ":" ? "\\:" : match;
    },
  );
}

export function parseMarkdown(text: string) {
  const known = parsedTexts.get(text);
  if (known) {
    known.render = renders.count;
    return known.parsed;
  }
  const parsed = Result.try({
    try: () => parseDocument(prepare(text)),
    catch: (cause) => new MarkdownFailed({ message: String(cause) }),
  });
  parsedTexts.set(text, { parsed, render: renders.count });
  return parsed;
}

const linkClass = "break-words text-primary underline underline-offset-2";
const insertedClass = "bg-green-500/40 no-underline";

// A text of a chat message has no "==" and "~~" of the diff. diff is true for the Brief diff of an action: briefMarkdown
// marks a removed line with "~~" and an added text with "==".
function markdownViews<M>(h: HtmlBuilder<M>, diff: boolean): Views {
  // Whether a "==" of the Brief diff has opened an added text that is not closed yet. The text can have inline code,
  // bold text or a link, so a "==" in one node can open an added text that ends in a later node of the same block. The
  // package renders the inline nodes of a block in order before the view of the block, so the block views close it.
  const mark = { open: false };

  const added = (shown: Html) =>
    diff && mark.open ? h.ins([h.Class(insertedClass)], [shown]) : shown;

  const text = (value: string) => {
    if (!diff) {
      return value;
    }
    return h.span(
      [],
      value.split("==").map((part, index) => {
        if (index > 0) {
          mark.open = !mark.open;
        }
        const shown = restoreEquals(part);
        return mark.open && part ? h.ins([h.Class(insertedClass)], [shown]) : shown;
      }),
    );
  };

  const link = (url: string, children: Array<Html | string>) =>
    h.a(
      [
        h.Href(url),
        h.Attribute("target", "_blank"),
        h.Attribute("rel", "noopener noreferrer"),
        h.Class(linkClass),
      ],
      children,
    );

  return {
    ...defaultViews,
    Text: ({ value }) => {
      const box = value[0];
      if (box !== checkedBox && box !== uncheckedBox) {
        return text(value);
      }
      return h.span(
        [],
        [
          h.input([h.Type("checkbox"), h.Readonly(true), h.Checked(box === checkedBox)]),
          text(value.slice(1)),
        ],
      );
    },
    InlineCode: ({ value }) => added(h.code([], [diff ? restoreEquals(value) : value])),
    HardBreak: () => added(h.br([])),
    Strikethrough: (_strikethrough, content) =>
      diff ? h.del([h.Class("bg-red-500/40 line-through")], content) : h.del([], content),
    Link: ({ url }, content) => link(url, [...content]),
    // A message does not load images, so an image shows as a link.
    Image: ({ url, alt }) => added(link(url, [alt || url])),
    Heading: (heading, content) => {
      mark.open = false;
      return defaultViews.Heading(heading, content);
    },
    Paragraph: (paragraph, content) => {
      mark.open = false;
      return defaultViews.Paragraph(paragraph, content);
    },
    TableCell: (cell, content, alignment, isHeader) => {
      mark.open = false;
      return defaultViews.TableCell(cell, content, alignment, isHeader);
    },
    CodeBlock: ({ value }) => h.pre([], [h.code([], [diff ? restoreEquals(value) : value])]),
  };
}

export const markdownClass =
  "grid grid-cols-[minmax(0,1fr)] gap-2 break-words [&_blockquote]:border-l-2 [&_blockquote]:pl-3 [&_blockquote]:text-muted-foreground [&_code]:rounded [&_code]:bg-muted [&_code]:px-1 [&_code]:font-mono [&_code]:text-[0.9em] [&_h1]:text-lg [&_h1]:font-semibold [&_h2]:text-base [&_h2]:font-semibold [&_h3]:font-semibold [&_hr]:border-border [&_ol]:list-decimal [&_ol]:pl-5 [&_p]:whitespace-pre-line [&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:bg-muted [&_pre]:p-2 [&_pre_code]:bg-transparent [&_pre_code]:p-0 [&_table]:block [&_table]:overflow-x-auto [&_td]:border [&_td]:px-2 [&_td]:py-1 [&_th]:border [&_th]:px-2 [&_th]:py-1 [&_th]:font-semibold [&_ul]:list-disc [&_ul]:pl-5";

export function renderMarkdown<M>(h: HtmlBuilder<M>, text: string, diff = false): Html {
  if (!text) {
    return h.empty;
  }
  const source = localTimes(text);
  return Result.match(parseMarkdown(source), {
    onSuccess: (document) =>
      h.div([h.Class(markdownClass)], viewBlocks(document, { views: markdownViews(h, diff) })),
    onFailure: () =>
      h.div([h.Class("whitespace-pre-line break-words")], [diff ? restoreEquals(source) : source]),
  });
}

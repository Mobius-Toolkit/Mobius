import type { Html, HtmlBuilder } from "foldkit/html";
import remarkGfm from "remark-gfm";
import remarkParse from "remark-parse";
import { unified } from "unified";
import { localTimes } from "@/lib/time";

const processor = unified().use(remarkParse).use(remarkGfm);

type Root = ReturnType<typeof processor.parse>;
type Node = Root["children"][number];
type Inline = Extract<Node, { type: "paragraph" }>["children"][number];
type Definition = Extract<Node, { type: "definition" }>;
type FootnoteDefinition = Extract<Node, { type: "footnoteDefinition" }>;
type List = Extract<Node, { type: "list" }>;
type ListItem = List["children"][number];
type Table = Extract<Node, { type: "table" }>;

type Entry = { root: Root; render: number };

const parsedTexts = new Map<string, Entry>();
const renders = { count: 0 };

// The view renders again after each Message, so the result of the parse of a text is kept. remark is the slow part.
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

export function parseMarkdown(text: string) {
  const known = parsedTexts.get(text);
  if (known) {
    known.render = renders.count;
    return known.root;
  }
  const root = processor.parse(text);
  parsedTexts.set(text, { root, render: renders.count });
  return root;
}

// briefMarkdown puts "==" around an added text. A "==" in the text of the Brief is shielded with a private-use
// character before the diff, so it cannot open an added text, and the views of the Brief diff restore it.
const shieldedEquals = "";

export function shieldEquals(text: string) {
  return text.replaceAll("==", shieldedEquals.repeat(2));
}

export function restoreEquals(text: string) {
  return text.replaceAll(shieldedEquals, "=");
}

const unsafeScheme = /^(javascript|vbscript|data):/i;

// A browser ignores the tabs, the new lines and the leading spaces of a URL, so "java\nscript:" is a script URL too.
function safeUrl(url: string) {
  const visible = [...url].filter((letter) => letter.charCodeAt(0) > 0x20).join("");
  return unsafeScheme.test(visible) ? undefined : url;
}

function plainText(node: Node | Inline): string {
  if ("value" in node) {
    return node.value;
  }
  if ("children" in node) {
    return node.children.map(plainText).join("");
  }
  return "";
}

const linkClass = "break-words text-primary underline underline-offset-2";

// A text of a chat message has no "==" and "~~" of the diff. diff is true for the Brief diff of an action: briefMarkdown
// marks a removed line with "~~" and an added text with "==".
type Context<M> = {
  h: HtmlBuilder<M>;
  diff: boolean;
  source: string;
  definitions: ReadonlyMap<string, Definition>;
  footnotes: ReadonlyMap<string, FootnoteDefinition>;
};

function collect<M>(root: Root, h: HtmlBuilder<M>, diff: boolean, source: string): Context<M> {
  const definitions = new Map<string, Definition>();
  const footnotes = new Map<string, FootnoteDefinition>();
  const walk = (nodes: readonly Node[]) => {
    for (const node of nodes) {
      if (node.type === "definition") {
        definitions.set(node.identifier, node);
      } else if (node.type === "footnoteDefinition") {
        footnotes.set(node.identifier, node);
      }
      if ("children" in node) {
        walk(node.children);
      }
    }
  };
  walk(root.children);
  return { h, diff, source, definitions, footnotes };
}

function link<M>(context: Context<M>, url: string, children: Array<Html | string>): Html {
  const { h } = context;
  const href = safeUrl(url);
  return h.a(
    [
      ...(href === undefined ? [] : [h.Href(href)]),
      h.Attribute("target", "_blank"),
      h.Attribute("rel", "noopener noreferrer"),
      h.Class(linkClass),
    ],
    children,
  );
}

// The text of a reference that has no definition shows as it is in the source.
function sourceOf<M>(context: Context<M>, node: Node | Inline) {
  const start = node.position?.start.offset;
  const end = node.position?.end.offset;
  return start === undefined || end === undefined
    ? plainText(node)
    : context.source.slice(start, end);
}

function inline<M>(context: Context<M>, node: Inline): Html | string {
  const { h, diff } = context;
  switch (node.type) {
    case "text":
      return diff
        ? h.span(
            [],
            // briefMarkdown puts "==" around each added text, and the text can have inline code, so a "=="
            // at the end of a node can open an added text that ends in the next node.
            node.value
              .split("==")
              .map((part, index) =>
                index % 2 === 1
                  ? h.ins([h.Class("bg-green-500/40 no-underline")], [restoreEquals(part)])
                  : restoreEquals(part),
              ),
          )
        : node.value;
    case "emphasis":
      return h.em([], content(context, node.children));
    case "strong":
      return h.strong([], content(context, node.children));
    case "delete":
      return diff
        ? h.del([h.Class("bg-red-500/40 line-through")], content(context, node.children))
        : h.del([], content(context, node.children));
    case "inlineCode":
      return h.code([], [diff ? restoreEquals(node.value) : node.value]);
    case "break":
      return h.br([]);
    case "link":
      return link(context, node.url, content(context, node.children));
    case "linkReference": {
      const definition = context.definitions.get(node.identifier);
      return definition
        ? link(context, definition.url, content(context, node.children))
        : sourceOf(context, node);
    }
    // A message does not load images, so an image shows as a link.
    case "image":
      return node.url ? link(context, node.url, [node.alt || node.url]) : node.alt || "";
    case "imageReference": {
      const definition = context.definitions.get(node.identifier);
      return definition
        ? link(context, definition.url, [node.alt || definition.url])
        : sourceOf(context, node);
    }
    case "footnoteReference":
      return context.footnotes.has(node.identifier)
        ? h.a([h.Href(`#${node.identifier}`)], [h.sup([], [node.label ?? node.identifier])])
        : sourceOf(context, node);
    // Raw HTML shows as text.
    case "html":
      return node.value;
    default:
      return plainText(node);
  }
}

function content<M>(context: Context<M>, nodes: readonly Inline[]): Array<Html | string> {
  return nodes.map((node) => inline(context, node));
}

function listItem<M>(context: Context<M>, list: List, item: ListItem): Html {
  const { h } = context;
  const tight = !list.spread && !item.spread;
  const checked = item.checked;
  return h.li(
    [],
    [
      ...(checked === null || checked === undefined
        ? []
        : [h.input([h.Type("checkbox"), h.Readonly(true), h.Checked(checked)]), " "]),
      ...item.children.flatMap((child) =>
        tight && child.type === "paragraph"
          ? content(context, child.children)
          : [block(context, child)],
      ),
    ],
  );
}

function table<M>(context: Context<M>, node: Table): Html {
  const { h } = context;
  const [head, ...body] = node.children;
  const cell = (tag: "th" | "td", index: number, children: readonly Inline[]) => {
    const align = node.align?.[index];
    return h[tag](align ? [h.Style({ textAlign: align })] : [], content(context, children));
  };
  const row = (tag: "th" | "td", cells: Table["children"][number]["children"]) =>
    h.tr(
      [],
      cells.map((item, index) => cell(tag, index, item.children)),
    );
  return h.table(
    [],
    [
      h.thead([], head ? [row("th", head.children)] : []),
      h.tbody(
        [],
        body.map((item) => row("td", item.children)),
      ),
    ],
  );
}

function block<M>(context: Context<M>, node: Node): Html {
  const { h, diff } = context;
  switch (node.type) {
    case "paragraph":
      return h.p([], content(context, node.children));
    case "heading": {
      const tag = (["h1", "h2", "h3", "h4", "h5", "h6"] as const)[node.depth - 1];
      return h[tag]([], content(context, node.children));
    }
    case "thematicBreak":
      return h.hr([]);
    case "blockquote":
      return h.blockquote(
        [],
        node.children.map((child) => block(context, child)),
      );
    case "list": {
      const items = node.children.map((item) => listItem(context, node, item));
      return node.ordered
        ? h.ol(
            node.start && node.start !== 1 ? [h.Attribute("start", String(node.start))] : [],
            items,
          )
        : h.ul([], items);
    }
    case "code":
      return h.pre(
        [],
        [
          h.code(node.lang ? [h.Class(`lang-${node.lang}`)] : [], [
            diff ? restoreEquals(node.value) : node.value,
          ]),
        ],
      );
    case "table":
      return table(context, node);
    // Raw HTML shows as text.
    case "html":
      return h.p([], [node.value]);
    // A definition and a footnote definition show where their references are.
    case "definition":
    case "footnoteDefinition":
      return h.empty;
    default:
      return h.p([], [plainText(node)]);
  }
}

export const markdownClass =
  "grid grid-cols-[minmax(0,1fr)] gap-2 break-words [&_blockquote]:border-l-2 [&_blockquote]:pl-3 [&_blockquote]:text-muted-foreground [&_code]:rounded [&_code]:bg-muted [&_code]:px-1 [&_code]:font-mono [&_code]:text-[0.9em] [&_h1]:text-lg [&_h1]:font-semibold [&_h2]:text-base [&_h2]:font-semibold [&_h3]:font-semibold [&_hr]:border-border [&_ol]:list-decimal [&_ol]:pl-5 [&_p]:whitespace-pre-line [&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:bg-muted [&_pre]:p-2 [&_pre_code]:bg-transparent [&_pre_code]:p-0 [&_table]:block [&_table]:overflow-x-auto [&_td]:border [&_td]:px-2 [&_td]:py-1 [&_th]:border [&_th]:px-2 [&_th]:py-1 [&_th]:font-semibold [&_ul]:list-disc [&_ul]:pl-5";

export function renderMarkdown<M>(h: HtmlBuilder<M>, text: string, diff = false): Html {
  if (!text) {
    return h.empty;
  }
  const source = localTimes(text);
  const root = parseMarkdown(source);
  const context = collect(root, h, diff, source);
  const footnotes = [...context.footnotes.values()];
  return h.div(
    [h.Class(markdownClass)],
    [
      ...root.children.map((node) => block(context, node)),
      ...(footnotes.length > 0
        ? [
            h.footer(
              [],
              footnotes.map((footnote) =>
                h.div(
                  [h.Id(footnote.identifier)],
                  [
                    `${footnote.label ?? footnote.identifier}: `,
                    ...footnote.children.flatMap((child) =>
                      child.type === "paragraph"
                        ? content(context, child.children)
                        : [block(context, child)],
                    ),
                  ],
                ),
              ),
            ),
          ]
        : []),
    ],
  );
}

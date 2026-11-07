import { parseMarkdown } from "@foldkit/markdown/vite";
import { Result } from "effect";
import { MarkdownFailed } from "./errors";

type Entry = { document: ReturnType<typeof parseMarkdown> | undefined; render: number };

const parsedTexts = new Map<string, Entry>();
const renders = { count: 0 };

const protectedText = /(```[\s\S]*?```|`[^`\n]*`|https?:\/\/\S+)/;

// The second parse escapes each colon between two word characters and each "<", but not in code and not in a URL.
// A backslash shows in code, and it ends a link.
function escapeForSecondParse(text: string) {
  return text
    .split(protectedText)
    .map((part, index) =>
      index % 2 === 1 ? part : part.replace(/(?<=\w):(?=\w)/g, "\\:").replace(/</g, "\\<"),
    )
    .join("");
}

const parse = (text: string) =>
  Result.try({
    try: () => parseMarkdown(text),
    catch: (cause) => new MarkdownFailed({ message: String(cause) }),
  });

// The view renders again after each Message, so the result of the parse of a text is kept, also the failure. remark is
// the slow part. @foldkit/markdown throws on each node outside its vocabulary (raw HTML, a link definition, a footnote,
// a "---" block at the start), and remark-directive is always on, so a text such as "at 09:30" also throws. The first
// parse reads the text as it is. The second parse escapes the colons and the "<" outside code and URLs. A text that
// fails both parses shows as plain text.
export function parseChatMarkdown(text: string) {
  const known = parsedTexts.get(text);
  if (known) {
    known.render = renders.count;
    return known.document;
  }
  const document = parse(text).pipe(
    Result.orElse(() => parse(escapeForSecondParse(text))),
    Result.getOrUndefined,
  );
  parsedTexts.set(text, { document, render: renders.count });
  return document;
}

// Each render calls this first. A text that the last render did not use leaves the cache, so the cache holds the texts
// of the chat on the screen, however long the chat is.
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
const shieldedEquals = "\uE000";

export function shieldEquals(text: string) {
  return text.replaceAll("==", shieldedEquals.repeat(2));
}

export function restoreEquals(text: string) {
  return text.replaceAll(shieldedEquals, "=");
}

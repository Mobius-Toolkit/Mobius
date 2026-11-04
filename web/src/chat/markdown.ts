import { parseMarkdown } from "@foldkit/markdown/vite";

const parsedTexts = new Map<string, ReturnType<typeof parseMarkdown> | null>();

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

// The view renders again after each Message, so the result of the parse of a text is kept, also the failure. remark is
// the slow part. @foldkit/markdown throws on each node outside its vocabulary (raw HTML, a link definition, a footnote,
// a "---" block at the start), and remark-directive is always on, so a text such as "at 09:30" also throws. The first
// parse reads the text as it is. The second parse escapes the colons and the "<" outside code and URLs. A text that
// fails both parses shows as plain text.
export function parseChatMarkdown(text: string) {
  if (parsedTexts.has(text)) {
    return parsedTexts.get(text) ?? undefined;
  }
  let document: ReturnType<typeof parseMarkdown> | null = null;
  try {
    document = parseMarkdown(text);
  } catch {
    try {
      document = parseMarkdown(escapeForSecondParse(text));
    } catch {
      document = null;
    }
  }
  if (parsedTexts.size >= 500) {
    parsedTexts.clear();
  }
  parsedTexts.set(text, document);
  return document ?? undefined;
}

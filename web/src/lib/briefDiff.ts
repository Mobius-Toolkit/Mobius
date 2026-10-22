import type { DiffLine, Part } from "@/lib/diff";

const removedMark = "~~";
const addedMark = "==";
const syntax = /[`*_~=[\]()<>#|\\]/;
const blockPrefix = /^\s*(?:>\s*)*(?:(?:[-*+]|\d+[.)])\s+)?(?:#{1,6}\s+)?/;
const fence = /^\s*(?:```|~~~)/;

const prefixLength = (line: string) => line.length - line.replace(blockPrefix, "").length;

function mark(text: string, delimiter: string) {
  const start = text.length - text.trimStart().length;
  const end = text.trimEnd().length;
  if (start >= end) {
    return text;
  }
  return text.slice(0, start) + delimiter + text.slice(start, end) + delimiter + text.slice(end);
}

function markLine(line: string, delimiter: string) {
  const start = prefixLength(line);
  const content = line.slice(start);
  return /[\p{L}\p{N}]/u.test(content) ? line.slice(0, start) + mark(content, delimiter) : line;
}

const insideCode = (line: string, at: number) =>
  (line.slice(0, at).match(/`/g) ?? []).length % 2 === 1;

function textOf(parts: Part[], skip: Part["kind"]) {
  return parts
    .filter((part) => part.kind !== skip)
    .map((part) => part.text)
    .join("");
}

// A mark cannot show in a Markdown syntax character, in the block prefix of a line or in inline code.
function markable(parts: Part[], removed: string, added: string) {
  let removedAt = 0;
  let addedAt = 0;
  for (const part of parts) {
    if (part.kind !== "unchanged") {
      const [line, at] = part.kind === "removed" ? [removed, removedAt] : [added, addedAt];
      if (syntax.test(part.text) || at < prefixLength(line) || insideCode(line, at)) {
        return false;
      }
    }
    if (part.kind !== "added") {
      removedAt += part.text.length;
    }
    if (part.kind !== "removed") {
      addedAt += part.text.length;
    }
  }
  return true;
}

function changedLines(parts: Part[]) {
  const removed = textOf(parts, "added");
  const added = textOf(parts, "removed");
  if (!markable(parts, removed, added)) {
    return [markLine(removed, removedMark), markLine(added, addedMark)];
  }
  return [
    parts
      .map((part) =>
        part.kind === "removed"
          ? mark(part.text, removedMark)
          : part.kind === "added"
            ? mark(part.text, addedMark)
            : part.text,
      )
      .join(""),
  ];
}

// Gives the Markdown of the new text with the removed text marked as ~~removed~~ and the added text as ==added==.
// A fenced code block shows the new lines with no mark.
export function briefMarkdown(diff: DiffLine[]): string {
  const out: string[] = [];
  let inFence = false;
  for (const line of diff) {
    if (line.kind === "removed") {
      if (!inFence && !fence.test(line.text) && line.text.trim() !== "") {
        out.push(markLine(line.text, removedMark));
      }
      continue;
    }
    const text = line.kind === "changed" ? textOf(line.parts, "removed") : line.text;
    if (fence.test(text)) {
      inFence = !inFence;
      out.push(text);
    } else if (inFence) {
      out.push(text);
    } else if (line.kind === "changed") {
      out.push(...changedLines(line.parts));
    } else {
      out.push(line.kind === "added" ? markLine(text, addedMark) : text);
    }
  }
  return out.join("\n");
}

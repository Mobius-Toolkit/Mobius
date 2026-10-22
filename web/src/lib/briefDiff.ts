import type { DiffLine, Part } from "@/lib/diff";

const removedMark = "~~";
const addedMark = "==";
const syntax = /[`*_~=[\]()<>#|\\]/;
const blockPrefix = /^\s*(?:>\s*)*(?:(?:[-*+]|\d+[.)])\s+)?(?:#{1,6}\s+)?/;
const url = /\]\([^)]*\)|https?:\/\/\S+|<[^>\s]+>/g;
const fence = /^\s*(?:```|~~~)/;
const tableRow = /^\s*\|/;
const listItem = /^\s*(?:[-*+]|\d+[.)])\s+/;
const quote = /^\s*>/;
const rule = /^\s*(?:[-=]+|(?:[-*_]\s*){3,})\s*$/;

const blockKinds = [listItem, quote, tableRow];

// Two lines are separate blocks when the new line is a rule or a setext underline, or when the removed line starts a list item, a quote or a table row that the new line does not continue.
const separate = (removed: string, added: string) =>
  rule.test(added) || blockKinds.some((kind) => kind.test(removed) && !kind.test(added));

const prefixLength = (line: string) => line.length - line.replace(blockPrefix, "").length;

function mark(text: string, delimiter: string) {
  const start = text.length - text.trimStart().length;
  const end = text.trimEnd().length;
  if (start >= end) {
    return text;
  }
  return text.slice(0, start) + delimiter + text.slice(start, end) + delimiter + text.slice(end);
}

function markLine(line: string, delimiter: string): string {
  if (tableRow.test(line)) {
    return line
      .split("|")
      .map((cell) => markLine(cell, delimiter))
      .join("|");
  }
  const start = prefixLength(line);
  const content = line.slice(start);
  return /[\p{L}\p{N}]/u.test(content) ? line.slice(0, start) + mark(content, delimiter) : line;
}

const insideCode = (line: string, at: number) =>
  (line.slice(0, at).match(/`/g) ?? []).length % 2 === 1;

const insideUrl = (line: string, at: number, length: number) =>
  [...line.matchAll(url)].some((m) => at < m.index + m[0].length && at + length > m.index);

function textOf(parts: Part[], skip: Part["kind"]) {
  return parts
    .filter((part) => part.kind !== skip)
    .map((part) => part.text)
    .join("");
}

// A mark cannot show in a Markdown syntax character, in the block prefix of a line, in inline code or in a URL.
function markable(parts: Part[], removed: string, added: string) {
  let removedAt = 0;
  let addedAt = 0;
  for (const part of parts) {
    if (part.kind !== "unchanged") {
      const [line, at] = part.kind === "removed" ? [removed, removedAt] : [added, addedAt];
      if (
        syntax.test(part.text) ||
        at < prefixLength(line) ||
        insideCode(line, at) ||
        insideUrl(line, at, part.text.length)
      ) {
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
    const markedRemoved = markLine(removed, removedMark);
    const markedAdded = markLine(added, addedMark);
    if (markedRemoved === removed) {
      return [markedAdded];
    }
    return separate(removed, added)
      ? [markedRemoved, "", markedAdded]
      : [markedRemoved, markedAdded];
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
// A removed blank line stays when a removed line is above or below it, and so does a removed table separator below a removed line.
// Thus a removed block keeps its shape.
// A blank line separates a removed line from a changed or added line when the two lines are separate blocks.
export function briefMarkdown(diff: DiffLine[]): string {
  const out: string[] = [];
  let inFence = false;
  let inOldFence = false;
  let afterRemoved = false;
  let lastRemoved = "";
  let removedBlank = false;
  for (const line of diff) {
    const old = line.kind === "changed" ? textOf(line.parts, "added") : line.text;
    const wasInOldFence = inOldFence;
    if (line.kind !== "added" && fence.test(old)) {
      inOldFence = !inOldFence;
    }
    if (line.kind === "removed") {
      const marked = markLine(line.text, removedMark);
      if (!inFence && !wasInOldFence && !fence.test(line.text)) {
        if (marked !== line.text) {
          if (removedBlank) {
            out.push("");
          }
          out.push(marked);
          afterRemoved = true;
          lastRemoved = line.text;
          removedBlank = false;
        } else if (afterRemoved && (line.text.trim() === "" || tableRow.test(line.text))) {
          out.push(line.text);
          afterRemoved = line.text.trim() !== "";
          lastRemoved = line.text;
        } else if (line.text.trim() === "") {
          removedBlank = true;
        }
      }
      continue;
    }
    const text = line.kind === "changed" ? textOf(line.parts, "removed") : line.text;
    if (afterRemoved && line.kind !== "unchanged" && separate(lastRemoved, text)) {
      out.push("");
    }
    afterRemoved = false;
    removedBlank = false;
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

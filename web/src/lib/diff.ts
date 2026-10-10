export type Part = { kind: "unchanged" | "added" | "removed"; text: string };

export type DiffLine =
  | { kind: "unchanged" | "added" | "removed"; text: string }
  | { kind: "changed"; parts: Part[] };

function lines(text: string) {
  return text === "" ? [] : text.replace(/\n$/, "").split("\n");
}

// Gives the parts of two lists, with the removed items before the added items of the same place.
function diffItems(a: string[], b: string[]): Part[] {
  const common = Array.from({ length: a.length + 1 }, () =>
    Array.from({ length: b.length + 1 }, () => 0),
  );
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      common[i][j] =
        a[i] === b[j] ? common[i + 1][j + 1] + 1 : Math.max(common[i + 1][j], common[i][j + 1]);
    }
  }
  const diff: Part[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length || j < b.length) {
    if (i < a.length && j < b.length && a[i] === b[j]) {
      diff.push({ kind: "unchanged", text: a[i] });
      i++;
      j++;
    } else if (i < a.length && (j === b.length || common[i + 1][j] >= common[i][j + 1])) {
      diff.push({ kind: "removed", text: a[i++] });
    } else {
      diff.push({ kind: "added", text: b[j++] });
    }
  }
  return diff;
}

// A word is a run of letters and digits. Each other character is a part of its own.
function words(line: string) {
  return line.match(/\s+|[\p{L}\p{N}_]+|[^\s\p{L}\p{N}_]/gu) ?? [];
}

// Joins the changes that only a space separates, so one changed phrase gives one removed part and one added part.
function joinChanges(parts: Part[]): Part[] {
  const joined: Part[] = [];
  let removed = "";
  let added = "";
  const flush = () => {
    if (removed) {
      joined.push({ kind: "removed", text: removed });
    }
    if (added) {
      joined.push({ kind: "added", text: added });
    }
    removed = "";
    added = "";
  };
  parts.forEach((part, i) => {
    const between =
      part.kind === "unchanged" &&
      /^\s+$/.test(part.text) &&
      i > 0 &&
      i < parts.length - 1 &&
      parts[i - 1].kind !== "unchanged" &&
      parts[i + 1].kind !== "unchanged";
    if (between) {
      removed += part.text;
      added += part.text;
    } else if (part.kind === "removed") {
      removed += part.text;
    } else if (part.kind === "added") {
      added += part.text;
    } else {
      flush();
      joined.push(part);
    }
  });
  flush();
  return joined;
}

function changedLine(removed: string, added: string): DiffLine | undefined {
  const parts = diffItems(words(removed), words(added));
  if (!parts.some((part) => part.kind === "unchanged" && /[\p{L}\p{N}]/u.test(part.text))) {
    return undefined;
  }
  return { kind: "changed", parts: joinChanges(parts) };
}

function changedRun(run: Part[]): DiffLine[] {
  const removed = run.filter((part) => part.kind === "removed");
  const added = run.filter((part) => part.kind === "added");
  const paired = Math.min(removed.length, added.length);
  const diff: DiffLine[] = [];
  for (let i = 0; i < paired; i++) {
    const changed = changedLine(removed[i].text, added[i].text);
    diff.push(...(changed ? [changed] : [removed[i], added[i]]));
  }
  return [...diff, ...removed.slice(paired), ...added.slice(paired)];
}

// Gives all lines of the two texts. A removed line and an added line of the same place with a common word are one
// changed line.
export function diffLines(before: string, after: string): DiffLine[] {
  const diff: DiffLine[] = [];
  let run: Part[] = [];
  for (const part of diffItems(lines(before), lines(after))) {
    if (part.kind === "unchanged") {
      diff.push(...changedRun(run), part);
      run = [];
    } else {
      run.push(part);
    }
  }
  return [...diff, ...changedRun(run)];
}

// Gives the changed lines in file order, each run with one unchanged line above and below it. Runs that touch are one
// hunk.
export function diffHunks(before: string, after: string): DiffLine[][] {
  const diff = diffLines(before, after);
  const ranges: { from: number; to: number }[] = [];
  diff.forEach((line, i) => {
    if (line.kind === "unchanged") {
      return;
    }
    const from = Math.max(i - 1, 0);
    const to = Math.min(i + 1, diff.length - 1);
    const last = ranges[ranges.length - 1];
    if (last && from <= last.to + 1) {
      last.to = to;
    } else {
      ranges.push({ from, to });
    }
  });
  return ranges.map(({ from, to }) => diff.slice(from, to + 1));
}

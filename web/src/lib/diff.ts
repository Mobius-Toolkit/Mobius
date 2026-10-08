export type DiffLine = { kind: "added" | "removed"; text: string };

function lines(text: string) {
  return text === "" ? [] : text.replace(/\n$/, "").split("\n");
}

// Gives the lines that differ between two texts, with the removed lines before the added lines of the same place.
export function diffLines(before: string, after: string): DiffLine[] {
  const a = lines(before);
  const b = lines(after);
  const common = Array.from({ length: a.length + 1 }, () =>
    Array.from({ length: b.length + 1 }, () => 0),
  );
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      common[i][j] =
        a[i] === b[j] ? common[i + 1][j + 1] + 1 : Math.max(common[i + 1][j], common[i][j + 1]);
    }
  }
  const diff: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length || j < b.length) {
    if (i < a.length && j < b.length && a[i] === b[j]) {
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

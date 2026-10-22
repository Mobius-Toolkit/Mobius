import type { DiffLine } from "@/lib/diff";
import { cn } from "@/lib/utils";

export function DiffRows({ hunk, wrap }: { hunk: DiffLine[]; wrap: boolean }) {
  return (
    <div className={cn(wrap ? "w-full" : "w-max min-w-full")}>
      {hunk.map((line, i) => (
        <div
          key={i}
          data-kind={line.kind}
          className={cn(
            "min-h-5 px-2",
            wrap ? "break-words whitespace-pre-wrap" : "whitespace-pre",
            line.kind === "added" && "bg-green-500/15 text-green-800 dark:text-green-300",
            line.kind === "removed" && "bg-red-500/15 text-red-800 dark:text-red-300",
          )}
        >
          {line.kind === "changed"
            ? line.parts.map((part, j) =>
                part.kind === "added" ? (
                  <ins key={j} className="bg-green-500/40 no-underline">
                    {part.text}
                  </ins>
                ) : part.kind === "removed" ? (
                  <del key={j} className="bg-red-500/40 no-underline">
                    {part.text}
                  </del>
                ) : (
                  part.text
                ),
              )
            : line.text}
        </div>
      ))}
    </div>
  );
}

import type { MarkdownToJSX } from "markdown-to-jsx/react";

// markdown-to-jsx renders ==text== as mark.
export const diffMarks: MarkdownToJSX.Overrides = {
  del: { props: { className: "bg-red-500/40 line-through" } },
  mark: { component: "ins", props: { className: "bg-green-500/40 no-underline" } },
};

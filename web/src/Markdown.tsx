import MarkdownToJSX from "markdown-to-jsx/react";
import { localTimes } from "@/lib/time";
import { Fragment, type ComponentProps } from "react";

function Link(props: ComponentProps<"a">) {
  return (
    <a
      {...props}
      target="_blank"
      rel="noopener noreferrer"
      className="break-words text-primary underline underline-offset-2"
    />
  );
}

// A message does not load images, so an image shows as a link.
function Image({ src, alt }: ComponentProps<"img">) {
  if (!src) {
    return alt;
  }
  return <Link href={src}>{alt || src}</Link>;
}

// Raw HTML in the text shows as text, and the sanitizer of the library removes unsafe URLs, for example javascript:.
export function Markdown({ text }: { text: string }) {
  return (
    <div className="grid grid-cols-[minmax(0,1fr)] gap-2 break-words [&_blockquote]:border-l-2 [&_blockquote]:pl-3 [&_blockquote]:text-muted-foreground [&_code]:rounded [&_code]:bg-muted [&_code]:px-1 [&_code]:font-mono [&_code]:text-[0.9em] [&_h1]:text-lg [&_h1]:font-semibold [&_h2]:text-base [&_h2]:font-semibold [&_h3]:font-semibold [&_hr]:border-border [&_ol]:list-decimal [&_ol]:pl-5 [&_p]:whitespace-pre-line [&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:bg-muted [&_pre]:p-2 [&_pre_code]:bg-transparent [&_pre_code]:p-0 [&_table]:block [&_table]:overflow-x-auto [&_td]:border [&_td]:px-2 [&_td]:py-1 [&_th]:border [&_th]:px-2 [&_th]:py-1 [&_th]:font-semibold [&_ul]:list-disc [&_ul]:pl-5">
      <MarkdownToJSX
        options={{
          disableParsingRawHTML: true,
          forceBlock: true,
          wrapper: Fragment,
          overrides: { a: Link, img: Image },
        }}
      >
        {localTimes(text)}
      </MarkdownToJSX>
    </div>
  );
}

import type { Html, HtmlBuilder } from "foldkit/html";
import { renderBadge } from "./badge";

// The lists go from edge to edge on the phone, so all other content of a page needs this inset.
export const inset = "px-4 md:px-0";

export const linkClass = "text-primary underline-offset-4 hover:underline";

// The phone shows the title of a page in the top bar.
export function renderPageHeader<M>(h: HtmlBuilder<M>, title: string): Html {
  return h.div(
    [h.Class("hidden items-center gap-2 md:flex")],
    [h.h2([h.Class("line-clamp-2 min-w-0 grow text-base font-semibold wrap-anywhere")], [title])],
  );
}

export function renderErrorBadge<M>(h: HtmlBuilder<M>, text: string): Html {
  return h.div([h.Class(inset)], [renderBadge(h, "destructive", text, "h-auto whitespace-normal")]);
}

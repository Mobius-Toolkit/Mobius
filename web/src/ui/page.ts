import type { Html, HtmlBuilder } from "foldkit/html";
import { renderBadge } from "./badge";
import { buttonClass } from "./button";
import { arrowLeftIcon } from "./icons";

// The lists go from edge to edge on the phone, so all other content of a page needs this inset.
export const inset = "px-4 md:px-0";

export const linkClass = "text-primary underline-offset-4 hover:underline";

export function renderBackButton<M>(h: HtmlBuilder<M>, back: M): Html {
  return h.button(
    [
      h.Type("button"),
      h.AriaLabel("Back"),
      h.DataAttribute("slot", "button"),
      h.Class(buttonClass({ variant: "ghost", size: "icon" })),
      h.OnClick(back),
    ],
    [arrowLeftIcon(h)],
  );
}

// The phone shows the title of a page in the top bar. back is the Message of the back button.
export function renderPageHeader<M>(h: HtmlBuilder<M>, title: string, back?: M): Html {
  return h.div(
    [h.Class("hidden items-center gap-2 md:flex")],
    [
      back === undefined ? h.empty : renderBackButton(h, back),
      h.h2([h.Class("line-clamp-2 min-w-0 grow text-base font-semibold wrap-anywhere")], [title]),
    ],
  );
}

export function renderErrorBadge<M>(h: HtmlBuilder<M>, text: string): Html {
  return h.div([h.Class(inset)], [renderBadge(h, "destructive", text, "h-auto whitespace-normal")]);
}

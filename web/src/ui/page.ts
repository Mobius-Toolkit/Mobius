import { cn } from "cn";
import type { Html, HtmlBuilder } from "foldkit/html";
import { renderBadge } from "./badge";
import { buttonClass } from "./button";
import { arrowLeftIcon, chevronRightIcon } from "./icons";

// The lists go from edge to edge on the phone, so all other content of a page needs this inset.
export const inset = "px-4 md:px-0";

export const linkClass = "text-primary underline-offset-4 hover:underline";

export const rowClass = "flex min-h-11 items-center gap-4 px-4 py-2 md:px-2";

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

// The phone shows the title of a page in the top bar. back is the Message of the back button, and action is a control
// after the title.
export function renderPageHeader<M>(
  h: HtmlBuilder<M>,
  title: string,
  back?: M,
  action?: Html,
): Html {
  return h.div(
    [h.Class("hidden items-center gap-2 md:flex")],
    [
      back === undefined ? h.empty : renderBackButton(h, back),
      h.h2([h.Class("line-clamp-2 min-w-0 grow text-base font-semibold wrap-anywhere")], [title]),
      action ?? h.empty,
    ],
  );
}

export function renderSection<M>(h: HtmlBuilder<M>, title: string, children: Html[]): Html {
  return h.keyed("section")(
    title,
    [h.Class("grid gap-1")],
    [
      h.h3(
        [h.Class(`text-xs font-semibold tracking-wide text-muted-foreground uppercase ${inset}`)],
        [title],
      ),
      ...children,
    ],
  );
}

// The negative margin on desktop puts the text of a row at the edge of the page.
export function renderList<M>(h: HtmlBuilder<M>, rows: Html[]): Html {
  return h.ul([h.Class("divide-y md:-mx-2")], rows);
}

export function renderRow<M>(h: HtmlBuilder<M>, key: string, className: string, children: Html[]) {
  return h.keyed("li")(key, [h.Class(cn(rowClass, className))], children);
}

// mount is the OnMount attribute of the router link Mount (defineRouterLink) for the path.
export function renderLinkRow<M>(
  h: HtmlBuilder<M>,
  options: {
    key: string;
    path: string;
    mount: ReturnType<HtmlBuilder<M>["OnMount"]>;
    title: Html;
    busy?: boolean;
    children?: Html[];
  },
): Html {
  return h.keyed("li")(
    options.key,
    [],
    [
      h.a(
        [
          h.Attribute("href", options.path),
          options.mount,
          ...(options.busy === undefined ? [] : [h.AriaBusy(options.busy)]),
          h.Class(cn(rowClass, "hover:bg-muted")),
        ],
        [
          h.span([h.Class("min-w-0 grow break-words")], [options.title]),
          ...(options.children ?? []),
          chevronRightIcon(h, "size-4 shrink-0"),
        ],
      ),
    ],
  );
}

export function renderErrorBadge<M>(h: HtmlBuilder<M>, text: string): Html {
  return h.div([h.Class(inset)], [renderBadge(h, "destructive", text, "h-auto whitespace-normal")]);
}

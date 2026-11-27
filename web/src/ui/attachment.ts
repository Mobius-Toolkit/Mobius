import { cn } from "cn";
import type { Html, HtmlBuilder } from "foldkit/html";
import { buttonClass } from "./button";
import { inboxIcon, xIcon } from "./icons";

type InboxItemFields = { reference?: string; title: string; reason?: string };

// The root is a button with onClick, a link with href, or else a div. onRemove adds the remove button and the width
// of a card in a composer.
export function renderAttachmentCard<Msg>(
  h: HtmlBuilder<Msg>,
  options: {
    kind: "selection" | "inbox-item" | "voice";
    key?: string;
    icon: Html;
    header: Html | string;
    body?: Html;
    onRemove?: Msg;
    removeLabel: string;
    onClick?: Msg;
    href?: string;
  },
): Html {
  const { kind, key, icon, header, body, onRemove, removeLabel, onClick, href } = options;
  const attributes = [
    ...(key ? [h.Key(key)] : []),
    h.DataAttribute("attachment-card", kind),
    h.Class(
      cn(
        "relative flex min-w-0 max-w-full items-start gap-2 rounded-lg border bg-card px-2 py-1.5 text-left text-sm",
        onRemove && "w-64 pr-9",
        (onClick || href) && "hover:bg-accent",
      ),
    ),
  ];
  const children = [
    h.span([h.Class("shrink-0 text-muted-foreground")], [icon]),
    h.span(
      [h.Class("grid min-w-0 flex-1 gap-0.5")],
      [h.span([h.Class("truncate text-xs text-muted-foreground")], [header]), body ?? h.empty],
    ),
    onRemove
      ? h.button(
          [
            h.Type("button"),
            h.AriaLabel(removeLabel),
            h.Attribute("title", removeLabel),
            h.DataAttribute("keep-focus", ""),
            h.Class(
              buttonClass({
                variant: "secondary",
                size: "icon-sm",
                className: "absolute top-0.5 right-0.5 rounded-full max-md:size-8",
              }),
            ),
            h.OnClick(onRemove),
          ],
          [xIcon(h)],
        )
      : h.empty,
  ];
  if (onClick) {
    return h.button([h.Type("button"), h.OnClick(onClick), ...attributes], children);
  }
  if (href) {
    return h.a(
      [
        h.Attribute("href", href),
        h.Attribute("target", "_blank"),
        h.Attribute("rel", "noreferrer"),
        ...attributes,
      ],
      children,
    );
  }
  return h.div(attributes, children);
}

// The reference is the header when the item has one. Else the title is the header.
export function inboxItemFields<Msg>(h: HtmlBuilder<Msg>, item: InboxItemFields) {
  const lines = [
    ...(item.reference ? [h.span([h.Class("truncate font-medium")], [item.title])] : []),
    ...(item.reason
      ? [h.span([h.Class("truncate text-xs text-muted-foreground")], [item.reason])]
      : []),
  ];
  return {
    icon: inboxIcon(h, "size-4"),
    header: item.reference || item.title,
    body: lines.length > 0 ? h.span([h.Class("grid gap-0.5")], lines) : undefined,
    removeLabel: `Remove ${item.reference || item.title}`,
  };
}

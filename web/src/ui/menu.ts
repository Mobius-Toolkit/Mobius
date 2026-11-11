import { cn } from "cn";
import type { Html, HtmlBuilder } from "foldkit/html";
import { buttonClass } from "./button";
import { checkIcon } from "./icons";

export const menuItemClass =
  "relative flex w-full cursor-default items-center gap-1.5 rounded-md py-1 pr-8 pl-1.5 text-left text-sm outline-hidden select-none hover:bg-accent hover:text-accent-foreground focus:bg-accent focus:text-accent-foreground [&_svg]:pointer-events-none [&_svg]:shrink-0";

// A button that opens a menu of items below it. className is for the box around the button. toggle is the Message of the
// button, and close is the Message of the layer behind the menu. The menu is as wide as the button, and at least 8 rem.
export function renderMenu<M>(
  h: HtmlBuilder<M>,
  options: {
    id: string;
    trigger: ReadonlyArray<Html | string>;
    className?: string;
    disabled?: boolean;
    open: boolean;
    toggle: M;
    close: M;
    items: ReadonlyArray<Html>;
  },
): Html {
  return h.div(
    [h.Class(cn("relative flex", options.className))],
    [
      h.button(
        [
          h.Type("button"),
          h.Id(options.id),
          h.Disabled(options.disabled ?? false),
          h.AriaHasPopup("menu"),
          h.AriaExpanded(options.open),
          h.DataAttribute("slot", "dropdown-menu-trigger"),
          h.Class(buttonClass({ variant: "outline" })),
          h.OnClick(options.toggle),
        ],
        options.trigger,
      ),
      options.open ? h.div([h.Class("fixed inset-0 z-40"), h.OnClick(options.close)]) : h.empty,
      options.open
        ? h.div(
            [
              h.Role("menu"),
              h.DataAttribute("slot", "dropdown-menu-content"),
              h.Class(
                "absolute top-full left-0 z-50 mt-1 max-h-60 w-full min-w-32 overflow-x-hidden overflow-y-auto rounded-lg bg-popover p-1 text-popover-foreground shadow-md ring-1 ring-foreground/10",
              ),
            ],
            options.items,
          )
        : h.empty,
    ],
  );
}

export function renderMenuItem<M>(
  h: HtmlBuilder<M>,
  options: {
    key: string;
    role: "menuitemcheckbox" | "menuitemradio";
    checked: boolean;
    title: string;
    onClick: M;
  },
): Html {
  return h.keyed("button")(
    options.key,
    [
      h.Type("button"),
      h.Role(options.role),
      h.AriaChecked(options.checked),
      h.Class(menuItemClass),
      h.OnClick(options.onClick),
    ],
    [
      options.title,
      options.checked
        ? h.span(
            [h.Class("pointer-events-none absolute right-2 flex items-center justify-center")],
            [checkIcon(h, "size-4")],
          )
        : h.empty,
    ],
  );
}

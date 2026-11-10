import type { Html, HtmlBuilder } from "foldkit/html";
import { buttonClass } from "./button";
import { checkIcon, chevronDownIcon, spinnerIcon } from "./icons";
import { menuItemClass } from "./menu";

// A button that opens a menu of values on one side of it. toggle is the Message of the button, and close is the Message of the
// layer behind the menu. The menu shows a spinner while loading is true.
export function renderChoice<M>(
  h: HtmlBuilder<M>,
  options: {
    id: string;
    value: string;
    values: readonly string[];
    loading: boolean;
    open: boolean;
    side: "above" | "below";
    toggle: M;
    close: M;
    pick: (value: string) => M;
  },
): Html {
  return h.div(
    [h.Class("relative flex min-w-0 grow")],
    [
      h.button(
        [
          h.Type("button"),
          h.Id(options.id),
          h.AriaHasPopup("menu"),
          h.AriaExpanded(options.open),
          h.DataAttribute("slot", "dropdown-menu-trigger"),
          h.Class(buttonClass({ variant: "outline", className: "min-w-0 grow justify-between" })),
          h.OnClick(options.toggle),
        ],
        [h.span([h.Class("truncate")], [options.value]), chevronDownIcon(h)],
      ),
      options.open ? h.div([h.Class("fixed inset-0 z-40"), h.OnClick(options.close)]) : h.empty,
      options.open
        ? h.div(
            [
              h.Role("menu"),
              h.DataAttribute("slot", "dropdown-menu-content"),
              h.Class(
                `absolute left-0 z-50 max-h-60 w-full min-w-32 overflow-x-hidden overflow-y-auto rounded-lg bg-popover p-1 text-popover-foreground shadow-md ring-1 ring-foreground/10 ${options.side === "above" ? "bottom-full mb-1" : "top-full mt-1"}`,
              ),
            ],
            options.loading
              ? [h.div([h.Class("flex justify-center p-2")], [spinnerIcon(h)])]
              : options.values.map((value) =>
                  h.keyed("button")(
                    value,
                    [
                      h.Type("button"),
                      h.Role("menuitemradio"),
                      h.AriaChecked(value === options.value),
                      h.Class(menuItemClass),
                      h.OnClick(options.pick(value)),
                    ],
                    [
                      value,
                      value === options.value
                        ? h.span(
                            [
                              h.Class(
                                "pointer-events-none absolute right-2 flex items-center justify-center",
                              ),
                            ],
                            [checkIcon(h, "size-4")],
                          )
                        : h.empty,
                    ],
                  ),
                ),
          )
        : h.empty,
    ],
  );
}

// A row of a label and its control. Children follow the control in the same cell.
export function renderField<M>(
  h: HtmlBuilder<M>,
  label: string,
  id: string,
  children: ReadonlyArray<Html>,
): Html {
  return h.div(
    [h.Class("grid grid-cols-[4rem_1fr] items-center gap-2")],
    [
      h.label(
        [
          h.For(id),
          h.DataAttribute("slot", "label"),
          h.Class("flex items-center gap-2 text-sm leading-none font-medium select-none"),
        ],
        [label],
      ),
      h.div([h.Class("flex min-w-0 items-center gap-2")], children),
    ],
  );
}

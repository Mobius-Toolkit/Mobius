import { cn } from "cn";
import type { Html, HtmlBuilder } from "foldkit/html";
import type { WorkstreamAutopilot } from "@/api/api.gen";
import { levelName, levels } from "@/lib/autopilot";
import { buttonClass } from "@/ui/button";
import { autopilotIcon, chevronDownIcon } from "@/ui/icons";
import { defineInPlaceMount, renderSheet } from "@/ui/sheet";
import { Message } from "./message";

const inPlaceMount = defineInPlaceMount(Message.CompletedFocusSheet);

function renderLevel<M>(
  h: HtmlBuilder<M>,
  value: WorkstreamAutopilot,
  name: string,
  text: string,
): Html {
  return h.span(
    [h.Class("flex min-w-0 gap-2 text-left")],
    [
      h.span([h.Class("mt-[3px] shrink-0")], [autopilotIcon(h, value)]),
      h.span(
        [h.Class("flex min-w-0 flex-col")],
        [h.span([], [name]), h.span([h.Class("text-xs text-muted-foreground")], [text])],
      ),
    ],
  );
}

export function renderAutopilotMenu(
  h: HtmlBuilder<Message>,
  level: WorkstreamAutopilot,
  disabled: boolean,
  open: boolean,
): Html {
  const name = `Autopilot: ${levelName(level)}`;

  return h.div(
    [h.Class("relative flex")],
    [
      h.button(
        [
          h.Type("button"),
          h.Disabled(disabled),
          h.AriaLabel(name),
          h.AriaHasPopup("menu"),
          h.AriaExpanded(open),
          h.DataAttribute("slot", "dropdown-menu-trigger"),
          h.Class(
            buttonClass({
              variant: "outline",
              size: "sm",
              className: "ml-auto shrink-0",
            }),
          ),
          h.OnClick(Message.ToggleMenu({ id: "autopilot" })),
        ],
        [autopilotIcon(h, level), levelName(level), chevronDownIcon(h, "size-4")],
      ),
      open ? h.div([h.Class("fixed inset-0 z-40"), h.OnClick(Message.CloseMenu())]) : h.empty,
      open
        ? h.div(
            [
              h.Role("menu"),
              h.DataAttribute("slot", "dropdown-menu-content"),
              h.Class(
                "absolute top-full right-0 z-50 mt-1 w-72 overflow-x-hidden overflow-y-auto rounded-lg bg-popover p-1 text-popover-foreground shadow-md ring-1 ring-foreground/10",
              ),
            ],
            levels.map((other) =>
              h.keyed("button")(
                other.value,
                [
                  h.Type("button"),
                  h.Role("menuitemradio"),
                  h.AriaChecked(other.value === level),
                  h.Class(
                    cn(
                      "relative flex w-full cursor-default items-start gap-2 rounded-md px-2 py-1.5 text-sm outline-hidden select-none hover:bg-accent hover:text-accent-foreground",
                      other.value === level && "bg-accent text-accent-foreground",
                    ),
                  ),
                  h.OnClick(Message.SelectAutopilotRequested({ level: other.value })),
                ],
                [renderLevel(h, other.value, other.name, other.text)],
              ),
            ),
          )
        : h.empty,
    ],
  );
}

export function renderAutopilotSheet(
  h: HtmlBuilder<Message>,
  level: WorkstreamAutopilot,
  disabled: boolean,
  open: boolean,
): Html {
  const name = `Autopilot: ${levelName(level)}`;

  return h.div(
    [h.Class("contents")],
    [
      h.button(
        [
          h.Type("button"),
          h.Disabled(disabled),
          h.AriaLabel(name),
          h.Title(name),
          h.DataAttribute("slot", "sheet-trigger"),
          h.Class(
            buttonClass({
              variant: "outline",
              size: "sm",
              className: "ml-auto shrink-0",
            }),
          ),
          h.OnClick(Message.ToggleMenu({ id: "autopilot-sheet" })),
        ],
        [autopilotIcon(h, level), chevronDownIcon(h, "size-4")],
      ),
      open
        ? renderSheet(h, {
            mount: h.OnMount(inPlaceMount()),
            titleId: "autopilot-sheet-title",
            title: "Autopilot",
            close: Message.CloseMenu(),
            className: "gap-2 p-4 pt-3",
            children: [
              h.div(
                [h.Role("radiogroup"), h.AriaLabel("Autopilot"), h.Class("flex flex-col")],
                levels.map((other) =>
                  h.button(
                    [
                      h.Key(other.value),
                      h.Type("button"),
                      h.Role("radio"),
                      h.AriaChecked(other.value === level),
                      h.Class(
                        cn(
                          "flex items-start gap-2 rounded-md px-2 py-2 text-left hover:bg-accent",
                          other.value === level && "bg-accent",
                        ),
                      ),
                      h.OnClick(Message.SelectAutopilotRequested({ level: other.value })),
                    ],
                    [renderLevel(h, other.value, other.name, other.text)],
                  ),
                ),
              ),
            ],
          })
        : h.empty,
    ],
  );
}

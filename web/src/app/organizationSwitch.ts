import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { renderBadge } from "@/ui/badge";
import { buttonClass } from "@/ui/button";
import { chevronDownIcon } from "@/ui/icons";
import { Message } from "./message";
import type { Model } from "./model";

export function renderOrganizationSwitch(h: HtmlBuilder<Message>, model: Model, id: string): Html {
  if (model.organizations.length <= 1) {
    return h.empty;
  }

  const organizations = model.organizations;
  const currentOrg = model.organization;
  const isOpen = Option.isSome(model.openMenu) && model.openMenu.value === id;

  // Calculate counts for each org
  const counts = new Map<string, number>();
  for (const item of model.inbox) {
    counts.set(item.organization, (counts.get(item.organization) ?? 0) + 1);
  }
  for (const chat of model.unread) {
    counts.set(chat.organization, (counts.get(chat.organization) ?? 0) + chat.count);
  }

  const elsewhere = organizations.some(
    (name) => name !== currentOrg && (counts.get(name) ?? 0) > 0,
  );

  return h.div(
    [h.Class("relative grid")],
    [
      h.button(
        [
          h.Type("button"),
          h.AriaHasPopup("menu"),
          h.AriaExpanded(isOpen),
          h.DataAttribute("slot", "dropdown-menu-trigger"),
          h.Class(
            buttonClass({
              variant: "outline",
              className: "md:w-full md:justify-start",
            }),
          ),
          h.OnClick(Message.ToggleMenu({ id })),
        ],
        [
          currentOrg,
          elsewhere
            ? h.span(
                [
                  h.AriaLabel("Work in another organization"),
                  h.Class("size-2 rounded-full bg-green-600"),
                ],
                [],
              )
            : h.empty,
          chevronDownIcon(h, "md:ml-auto size-4"),
        ],
      ),
      isOpen ? h.div([h.Class("fixed inset-0 z-40"), h.OnClick(Message.CloseMenu())]) : h.empty,
      isOpen
        ? h.div(
            [
              h.Role("menu"),
              h.DataAttribute("slot", "dropdown-menu-content"),
              h.Class(
                "absolute top-full left-0 z-50 mt-1 max-h-60 w-full min-w-32 overflow-x-hidden overflow-y-auto rounded-lg bg-popover p-1 text-popover-foreground shadow-md ring-1 ring-foreground/10 md:w-auto md:min-w-56",
              ),
            ],
            [
              h.div(
                [
                  h.Class(
                    "px-2 py-1.5 text-xs font-semibold tracking-wide text-muted-foreground uppercase",
                  ),
                ],
                ["Organizations"],
              ),
              ...organizations.map((name) => {
                const count = counts.get(name) ?? 0;
                const showBadge = name !== currentOrg && count > 0;
                return h.keyed("button")(
                  name,
                  [
                    h.Type("button"),
                    h.Role("menuitemradio"),
                    h.AriaChecked(name === currentOrg),
                    h.Class(
                      "relative flex w-full cursor-default items-center gap-1.5 rounded-md px-2 py-1.5 text-left text-sm outline-hidden select-none hover:bg-accent hover:text-accent-foreground",
                    ),
                    h.OnClick(Message.SelectOrganization({ organization: name })),
                  ],
                  [
                    h.span([h.Class("grow")], [name]),
                    showBadge ? renderBadge(h, "default", String(count)) : h.empty,
                  ],
                );
              }),
            ],
          )
        : h.empty,
    ],
  );
}

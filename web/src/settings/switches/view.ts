import type { Html, HtmlBuilder } from "foldkit/html";
import { switchKinds } from "@/lib/switches";
import { renderBadge } from "@/ui/badge";
import { renderList, renderPageHeader, renderRow } from "@/ui/page";
import { renderSwitch } from "@/ui/switch";
import { Message } from "./message";
import { isOn, shownError, type Model } from "./model";

type H = HtmlBuilder<Message>;

function renderSwitchRow(h: H, model: Model, entry: (typeof switchKinds)[number]): Html {
  const error = shownError(model, entry.kind);
  const on = isOn(model, entry.kind);
  return renderRow(h, entry.kind, "", [
    h.div(
      [h.Class("grid min-w-0 grow gap-0.5")],
      [
        h.label(
          [
            h.For(`switch-${entry.kind}`),
            h.DataAttribute("slot", "label"),
            h.Class("flex items-center gap-2 text-sm leading-none font-medium select-none"),
          ],
          [entry.title],
        ),
        h.span([h.Class("text-sm text-muted-foreground")], [entry.description]),
        error
          ? renderBadge(h, "destructive", error, "h-auto justify-self-start whitespace-normal")
          : h.empty,
      ],
    ),
    renderSwitch(h, {
      id: `switch-${entry.kind}`,
      checked: on,
      disabled: false,
      toggle: Message.ClickedSwitch({ kind: entry.kind, on: !on }),
    }),
  ]);
}

export function view(model: Model, h: H): Html {
  return h.div(
    [h.Class("grid gap-6")],
    [
      renderPageHeader(h, "Switches", Message.ClickedBack()),
      renderList(
        h,
        switchKinds.map((entry) => renderSwitchRow(h, model, entry)),
      ),
    ],
  );
}

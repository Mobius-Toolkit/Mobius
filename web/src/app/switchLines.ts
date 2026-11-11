import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import type { HeldWork } from "@/api/api.gen";
import { switchKinds } from "@/lib/switches";
import { renderButton } from "@/ui/button";
import { renderErrorBadge } from "@/ui/page";
import { defineInPlaceMount, renderSheet } from "@/ui/sheet";
import { Message } from "./message";
import type { Model } from "./model";

const inPlaceMount = defineInPlaceMount(Message.CompletedFocusSheet);

function renderHeldSheet(h: HtmlBuilder<Message>, title: string, held: readonly HeldWork[]): Html {
  return renderSheet(h, {
    mount: h.OnMount(inPlaceMount()),
    titleId: "held-sheet-title",
    title,
    close: Message.CloseHeldSheet(),
    className: "gap-2 p-4 pt-3",
    children: [
      held.length === 0
        ? h.p([h.Class("text-muted-foreground")], ["Nothing waits."])
        : h.ul(
            [h.Class("max-h-[60svh] divide-y overflow-y-auto")],
            held.map((item) => {
              const [owner, name] = item.workstreamRepository.split("/");
              const path = `/workstreams/${owner}/${name}/${item.workstream}`;
              const issueLabel =
                item.repository === item.workstreamRepository
                  ? `#${item.issue}`
                  : `${item.repository}#${item.issue}`;

              return h.li(
                [h.Key(`${item.repository}#${item.issue}`)],
                [
                  h.a(
                    [
                      h.Attribute("href", path),
                      h.Class("grid gap-0.5 rounded-md px-2 py-2 hover:bg-accent"),
                      h.OnClick(Message.CloseHeldSheet()),
                    ],
                    [
                      h.span([h.Class("wrap-anywhere")], [`${issueLabel} ${item.title}`]),
                      h.span(
                        [h.Class("text-xs text-muted-foreground wrap-anywhere")],
                        [item.workstreamTitle],
                      ),
                    ],
                  ),
                ],
              );
            }),
          ),
    ],
  });
}

export function renderSwitchLines(h: HtmlBuilder<Message>, model: Model): Html {
  if (Option.isNone(model.switches)) {
    return h.empty;
  }

  const switches = model.switches.value;
  const offKinds = switchKinds.filter(({ kind }) => !switches[kind]);
  if (offKinds.length === 0 && Option.isNone(model.heldSheet)) {
    return h.empty;
  }

  const items = offKinds.map(({ kind, line }) => {
    const held = switches.held.filter((item) => item.kind === kind);
    const isBusy = Option.isSome(model.switchBusy) && model.switchBusy.value === kind;
    const error = model.switchErrors[kind];

    return h.div(
      [h.Key(kind), h.Class("grid gap-2")],
      [
        h.div(
          [
            h.Class(
              "flex flex-wrap items-center justify-between gap-2 rounded-lg border border-amber-500/40 bg-amber-100 px-3 py-2 text-sm text-amber-900 dark:bg-amber-950 dark:text-amber-200",
            ),
          ],
          [
            h.span([], [line]),
            h.span(
              [h.Class("flex items-center gap-2")],
              [
                renderButton(h, {
                  label: [String(held.length), " held"],
                  size: "sm",
                  variant: "outline",
                  expanded: Option.isSome(model.heldSheet) && model.heldSheet.value.title === line,
                  onClick: Message.OpenHeldSheet({ title: line, held }),
                }),
                renderButton(h, {
                  label: "Resume",
                  size: "sm",
                  disabled: isBusy,
                  pending: isBusy,
                  onClick: Message.ToggleSwitchRequested({ kind, on: true }),
                }),
              ],
            ),
          ],
        ),
        error ? renderErrorBadge(h, error) : h.empty,
      ],
    );
  });

  return h.div(
    [h.Class("grid gap-2")],
    [
      ...items,
      Option.isSome(model.heldSheet)
        ? renderHeldSheet(h, model.heldSheet.value.title, model.heldSheet.value.held)
        : h.empty,
    ],
  );
}

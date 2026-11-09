import { cn } from "cn";
import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { defineView } from "foldkit/submodel";
import { bindingText } from "@/lib/harness";
import * as Setting from "@/setting/view";
import { renderBadge } from "@/ui/badge";
import { buttonClass } from "@/ui/button";
import { playIcon, settingsIcon, spinnerIcon, squareIcon } from "@/ui/icons";
import { renderSwitch } from "@/ui/switch";
import { Message } from "./message";
import {
  reference,
  resumes,
  rowKey,
  rowOf,
  stops,
  withoutClosed,
  type Model,
  type TaskLine,
} from "./model";

type H = HtmlBuilder<Message>;

function renderIconButton(
  h: H,
  options: { label: string; disabled: boolean; busy: boolean; icon: Html; click: Message },
): Html {
  return h.button(
    [
      h.Type("button"),
      h.AriaLabel(options.label),
      h.Title(options.label),
      h.Disabled(options.disabled),
      h.Class(buttonClass({ variant: "ghost", size: "icon", className: "shrink-0" })),
      h.OnClick(options.click),
    ],
    [options.busy ? spinnerIcon(h) : options.icon],
  );
}

function startLabel(line: TaskLine) {
  if (resumes(line)) {
    return `Resume ${reference(line)}`;
  }
  return `Start ${reference(line)}${line.blockedBy.length > 0 ? " when unblocked" : ""}`;
}

function renderEntry(h: H, model: Model, line: TaskLine): Html {
  const key = rowKey(line);
  const row = rowOf(model, key);
  const label = startLabel(line);
  const stopLabel = `Stop ${reference(line)}`;
  const settingLabel = `Implementer of ${reference(line)}`;
  const fallback = Option.isSome(model.fallback);
  return h.keyed("li")(
    key,
    [h.Class("grid gap-1")],
    [
      h.div(
        [h.Class("flex items-center gap-1")],
        [
          h.a(
            [
              h.Href(line.url),
              h.Attribute("target", "_blank"),
              h.Attribute("rel", "noreferrer"),
              h.Style({ paddingLeft: `${0.5 + line.depth * 1.25}rem` }),
              h.Class(
                "flex min-w-0 grow flex-wrap items-center gap-x-2 gap-y-1 rounded-lg py-2 pr-2 hover:bg-muted",
              ),
            ],
            [
              h.span(
                [
                  h.Class(
                    cn(
                      "min-w-0 grow wrap-anywhere",
                      line.state === "closed" && "text-muted-foreground",
                    ),
                  ),
                ],
                [`${reference(line)} ${line.title}`],
              ),
              line.implementer
                ? h.span(
                    [h.Class("w-full text-sm text-muted-foreground wrap-anywhere")],
                    [bindingText(line.implementer)],
                  )
                : h.empty,
              ...line.blockedBy.map((blocker) =>
                h.keyed("span")(
                  `${blocker.repository ?? ""}#${blocker.number}`,
                  [h.Class("min-w-0 text-sm text-muted-foreground wrap-anywhere")],
                  [
                    `blocked by ${blocker.repository ?? ""}#${blocker.number}${
                      blocker.workstreamTitle ? ` (Workstream "${blocker.workstreamTitle}")` : ""
                    }`,
                  ],
                ),
              ),
              renderBadge(h, line.state === "open" ? "outline" : "secondary", line.state),
            ],
          ),
          line.state !== "closed" && fallback
            ? renderIconButton(h, {
                label: settingLabel,
                disabled: false,
                busy: false,
                icon: settingsIcon(h),
                click: Message.ClickedSetting({ line }),
              })
            : h.empty,
          h.div(
            [h.Class(cn("flex shrink-0 justify-end", line.state !== "closed" && "size-8"))],
            [
              line.state === "open" ||
              line.state === "triage" ||
              (resumes(line) && line.blockedBy.length === 0)
                ? renderIconButton(h, {
                    label,
                    disabled: row.starting,
                    busy: row.starting,
                    icon: playIcon(h),
                    click: Message.ClickedStart({ key, line }),
                  })
                : h.empty,
              stops(line)
                ? renderIconButton(h, {
                    label: stopLabel,
                    disabled: row.stopping,
                    busy: row.stopping,
                    icon: squareIcon(h),
                    click: Message.ClickedStop({ key, line }),
                  })
                : h.empty,
            ],
          ),
        ],
      ),
      row.error
        ? renderBadge(h, "destructive", row.error, "h-auto w-full justify-start whitespace-normal")
        : h.empty,
    ],
  );
}

export const view = defineView<Model, Message>((model, h) => {
  const shown = Option.map(model.lines, (lines) =>
    model.showClosed ? lines : withoutClosed(lines),
  );
  const closedId = `${model.instance}-closed-tasks`;
  return h.div(
    [h.Class("grid gap-2")],
    [
      model.error ? renderBadge(h, "destructive", model.error) : h.empty,
      h.div(
        [h.Class("flex items-center gap-2 px-2")],
        [
          renderSwitch(h, {
            id: closedId,
            checked: model.showClosed,
            disabled: false,
            toggle: Message.ToggledClosed(),
          }),
          h.label(
            [
              h.For(closedId),
              h.DataAttribute("slot", "label"),
              h.Class("flex items-center gap-2 text-sm leading-none font-medium select-none"),
            ],
            ["Show closed tasks"],
          ),
        ],
      ),
      Option.exists(shown, (lines) => lines.length === 0)
        ? h.p([h.Class("px-2 text-sm text-muted-foreground")], ["No tasks."])
        : h.empty,
      h.ul(
        [],
        Option.getOrElse(shown, () => []).map((line) => renderEntry(h, model, line)),
      ),
      Option.match(model.sheet, {
        onNone: () => h.empty,
        onSome: (sheet) =>
          h.submodel({
            slotId: "setting",
            model: sheet,
            view: Setting.view,
            toParentMessage: (message) => Message.GotSettingMessage({ message }),
          }),
      }),
    ],
  );
});

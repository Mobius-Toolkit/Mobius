import { cn } from "cn";
import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { defineView } from "foldkit/submodel";
import { dayClock } from "@/lib/time";
import { renderBadge } from "@/ui/badge";
import { buttonClass } from "@/ui/button";
import { defineDialogMount, renderDialog } from "@/ui/dialog";
import { spinnerIcon } from "@/ui/icons";
import { Message } from "./message";
import type { Deletion, Model, RecurringTask } from "./model";

type H = HtmlBuilder<Message>;
type Run = RecurringTask["runs"][number];

const DialogOpened = defineDialogMount(Message.CompletedFocusDialog);

const resultText = (run: Run) =>
  run.result === "event" ? "event" : run.reason ? `skipped: ${run.reason}` : "skipped";

function renderHistory(h: H, task: RecurringTask): Html {
  return h.div(
    [h.Class("grid gap-1 px-2 text-sm")],
    [
      h.h3([h.Class("font-medium")], ["History"]),
      task.runs.length === 0
        ? h.p([h.Class("text-muted-foreground")], ["No run yet"])
        : h.ul(
            [h.Class("grid gap-1")],
            task.runs.map((run) =>
              h.keyed("li")(
                run.time,
                [h.Class("grid grid-cols-[auto_1fr] gap-x-4")],
                [
                  h.span([h.Class("whitespace-nowrap")], [dayClock(run.time)]),
                  h.span([h.Class("min-w-0 wrap-anywhere")], [resultText(run)]),
                ],
              ),
            ),
          ),
    ],
  );
}

function renderTask(h: H, model: Model, task: RecurringTask): Html {
  const open = Option.exists(model.opened, (id) => id === task.id);
  const busy = model.busy.includes(task.id);
  const last = task.runs[0];
  return h.keyed("li")(
    String(task.id),
    [h.Class("grid gap-2 py-2")],
    [
      h.button(
        [
          h.Type("button"),
          h.AriaExpanded(open),
          h.Class("grid min-w-0 gap-1 rounded-lg px-2 py-1 text-left hover:bg-muted"),
          h.OnClick(Message.ClickedRow({ id: task.id })),
        ],
        [
          h.span(
            [h.Class("flex items-start justify-between gap-2")],
            [
              h.span([h.Class(cn("min-w-0 wrap-anywhere", !open && "line-clamp-2"))], [task.text]),
              task.paused ? renderBadge(h, "secondary", "Paused", "shrink-0") : h.empty,
            ],
          ),
          h.span(
            [h.Class("text-sm text-muted-foreground wrap-anywhere")],
            [task.next === null ? task.cron : `${task.cron} · Next ${dayClock(task.next)}`],
          ),
          last
            ? h.span(
                [h.Class("text-sm text-muted-foreground wrap-anywhere")],
                [`Last: ${dayClock(last.time)} ${resultText(last)}`],
              )
            : h.empty,
        ],
      ),
      h.div(
        [h.Class("flex justify-end gap-2 px-2")],
        [
          h.button(
            [
              h.Type("button"),
              h.Disabled(busy),
              h.Class(buttonClass({ variant: "outline", size: "sm" })),
              h.OnClick(Message.ClickedPause({ id: task.id, paused: !task.paused })),
            ],
            [busy ? spinnerIcon(h) : h.empty, task.paused ? "Resume" : "Pause"],
          ),
          h.button(
            [
              h.Type("button"),
              h.Class(buttonClass({ variant: "outline", size: "sm" })),
              h.OnClick(Message.ClickedDelete({ id: task.id, text: task.text })),
            ],
            ["Delete"],
          ),
        ],
      ),
      open ? renderHistory(h, task) : h.empty,
    ],
  );
}

function renderConfirmation(h: H, deletion: Deletion): Html {
  return renderDialog(h, {
    mount: h.OnMount(DialogOpened()),
    titleId: "recurring-delete-title",
    title: "Delete the recurring task",
    description: "The history of the task goes away with it.",
    close: Message.DismissedDelete(),
    body: h.p([h.Class("px-4 pb-4 wrap-anywhere")], [deletion.text]),
    footer: h.div(
      [
        h.DataAttribute("slot", "dialog-footer"),
        h.Class(
          "flex flex-col-reverse gap-2 rounded-b-xl border-t bg-muted/50 p-4 sm:flex-row sm:justify-end",
        ),
      ],
      [
        h.button(
          [
            h.Type("button"),
            h.Class(buttonClass({ variant: "outline" })),
            h.OnClick(Message.DismissedDelete()),
          ],
          ["Cancel"],
        ),
        h.button(
          [
            h.Type("button"),
            h.Disabled(deletion.busy),
            h.Class(buttonClass({ variant: "destructive" })),
            h.OnClick(Message.ClickedConfirmDelete()),
          ],
          [deletion.busy ? spinnerIcon(h) : h.empty, "Delete"],
        ),
      ],
    ),
  });
}

export const view = defineView<Model, Message>((model, h) => {
  const tasks = Option.getOrUndefined(model.tasks);
  return h.div(
    [h.Class("grid gap-2")],
    [
      model.error ? renderBadge(h, "destructive", model.error) : h.empty,
      tasks && tasks.length === 0
        ? h.p(
            [h.Class("px-2 text-muted-foreground")],
            ["No recurring task. Ask the Lead in the chat to add one."],
          )
        : h.empty,
      tasks
        ? h.ul(
            [h.Class("grid divide-y")],
            tasks.map((task) => renderTask(h, model, task)),
          )
        : h.empty,
      Option.match(model.deletion, {
        onNone: () => h.empty,
        onSome: (deletion) => renderConfirmation(h, deletion),
      }),
    ],
  );
});

import { cn } from "cn";
import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { defineView } from "foldkit/submodel";
import { clock, localTimes } from "@/lib/time";
import { renderBadge } from "@/ui/badge";
import { buttonClass } from "@/ui/button";
import { renderConnecting } from "@/ui/connection";
import { arrowDownIcon, spinnerIcon } from "@/ui/icons";
import { inset, renderBackButton } from "@/ui/page";
import { Message } from "./message";
import type { Model } from "./model";
import { TranscriptLog } from "./mount";
import type { Line } from "./schemas";

type H = HtmlBuilder<Message>;

function renderEntry(h: H, model: Model, line: Line): Html {
  const open = line.folded === model.toggledOpen.includes(line.id);
  const raw = model.rawShown.includes(line.id);
  return h.keyed("li")(
    String(line.id),
    [
      h.DataAttribute("line", String(line.id)),
      h.Class(
        cn(
          "grid max-w-[85%] grid-cols-[minmax(0,1fr)] gap-1 rounded-xl border px-3 py-2",
          line.kind === "prompt"
            ? "justify-self-end border-transparent bg-secondary"
            : "justify-self-start bg-card",
          line.error && "text-destructive",
        ),
      ),
    ],
    [
      h.div(
        [h.Class("flex gap-2 text-xs text-muted-foreground")],
        [h.span([h.Class("font-mono")], [line.kind]), h.span([], [clock(line.time)])],
      ),
      h.span(
        [h.Class("break-words")],
        [
          localTimes(line.text),
          line.harnessToolName
            ? h.span([h.Class("text-sm text-muted-foreground")], [` ${line.harnessToolName}`])
            : h.empty,
        ],
      ),
      h.span(
        [h.Class("flex gap-1")],
        [
          line.folded && line.body
            ? h.button(
                [
                  h.Type("button"),
                  h.Class(buttonClass({ variant: "ghost", size: "xs" })),
                  h.OnClick(Message.ToggledOpen({ id: line.id })),
                ],
                [open ? "Hide" : "Show"],
              )
            : h.empty,
          h.button(
            [
              h.Type("button"),
              h.Class(buttonClass({ variant: "ghost", size: "xs" })),
              h.OnClick(Message.ToggledRaw({ id: line.id })),
            ],
            ["Raw"],
          ),
        ],
      ),
      open && line.body
        ? h.pre(
            [h.Class("overflow-x-auto rounded-md bg-muted p-2 text-xs whitespace-pre-wrap")],
            [localTimes(line.body)],
          )
        : h.empty,
      raw
        ? h.pre([h.Class("overflow-x-auto rounded-md bg-muted p-2 text-xs")], [line.raw])
        : h.empty,
    ],
  );
}

function renderStartCheck(h: H, model: Model): Html[] {
  if (!model.startable) {
    return [];
  }
  return [
    h.button(
      [
        h.Type("button"),
        h.Disabled(model.starting),
        h.Class(buttonClass({ variant: "outline" })),
        h.OnClick(Message.ClickedStartCheck()),
      ],
      [model.starting ? spinnerIcon(h) : h.empty, "Start the check now"],
    ),
    model.startError
      ? renderBadge(h, "destructive", model.startError, "h-auto justify-start whitespace-normal")
      : h.empty,
  ];
}

function renderLog(h: H, model: Model, className: string): Html {
  const lines = Option.getOrElse(model.lines, () => []);
  return h.div(
    [h.Class(cn("flex min-h-0 grow flex-col gap-4", className))],
    [
      model.error ? renderBadge(h, "destructive", model.error) : h.empty,
      h.div(
        [h.Class("relative flex min-h-10 grow flex-col")],
        [
          h.keyed("ul")(
            String(model.agent.id),
            [
              h.OnMount(TranscriptLog()),
              h.DataAttribute("version", String(model.version)),
              h.DataAttribute("jumps", String(model.jumps)),
              h.Class(
                "grid min-h-0 grow grid-cols-[minmax(0,1fr)] content-start gap-2 overflow-y-auto",
              ),
            ],
            lines.map((line) => renderEntry(h, model, line)),
          ),
          model.opened
            ? h.empty
            : h.div([h.Class("absolute inset-x-0 top-0")], [renderConnecting(h)]),
          model.behind
            ? h.button(
                [
                  h.Type("button"),
                  h.Class(
                    buttonClass({
                      variant: "secondary",
                      size: "sm",
                      className:
                        "absolute bottom-2 left-1/2 -translate-x-1/2 rounded-full shadow-md",
                    }),
                  ),
                  h.OnClick(Message.JumpedToEnd()),
                ],
                [arrowDownIcon(h), "New messages"],
              )
            : h.empty,
        ],
      ),
      h.p(
        [h.Class("text-sm text-muted-foreground")],
        ["Read only. The Owner talks only to the Lead."],
      ),
    ],
  );
}

const title = (model: Model) => `${model.agent.name} ${model.agent.title}`;

export const viewPanel = defineView<Model, Message>((model, h) =>
  h.div(
    [h.Class("flex min-h-0 grow flex-col gap-2")],
    [
      h.div(
        [h.Class("flex items-center gap-2")],
        [
          renderBackButton(h, Message.ClickedBack()),
          h.div(
            [h.Class("line-clamp-2 min-w-0 grow text-base leading-snug font-medium")],
            [title(model)],
          ),
        ],
      ),
      h.div(
        [h.Class("flex flex-wrap items-center gap-2 empty:hidden")],
        renderStartCheck(h, model),
      ),
      renderLog(h, model, ""),
    ],
  ),
);

export const viewPage = defineView<Model, Message>((model, h) =>
  h.div(
    [h.Class("contents")],
    [
      h.div(
        [h.Class(cn(inset, "flex flex-wrap items-center gap-2 empty:hidden"))],
        renderStartCheck(h, model),
      ),
      renderLog(
        h,
        model,
        cn(inset, "max-md:max-h-[calc(100svh-10rem)] md:max-h-[calc(100svh-9.875rem)]"),
      ),
    ],
  ),
);

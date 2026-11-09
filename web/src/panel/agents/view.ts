import { cn } from "cn";
import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { defineView } from "foldkit/submodel";
import { queueState, queueText } from "@/lib/agents";
import { bindingText } from "@/lib/harness";
import { clock, dayClock } from "@/lib/time";
import * as Transcript from "@/transcript/view";
import { renderBadge } from "@/ui/badge";
import { renderSwitch } from "@/ui/switch";
import { Message } from "./message";
import { shownAgents, type Model } from "./model";
import type { Agent } from "@/transcript/schemas";

type H = HtmlBuilder<Message>;

function renderEntry(h: H, agent: Agent): Html {
  const state = queueState(agent.queueReason);
  const started = `${dayClock(agent.startedAt)}${agent.endedAt ? `–${clock(agent.endedAt)}` : ""}`;
  const detail = agent.queueReason ? `${started} · ${queueText(agent)}` : started;
  return h.keyed("li")(
    String(agent.id),
    [],
    [
      h.button(
        [
          h.Type("button"),
          h.Class("flex w-full items-center gap-3 rounded-lg p-2 text-left hover:bg-muted"),
          h.OnClick(Message.OpenedAgent({ agent })),
        ],
        [
          h.span(
            [
              h.Class(
                cn(
                  "size-2 shrink-0 rounded-full",
                  agent.endedAt ? "border border-muted-foreground" : state.dot,
                ),
              ),
            ],
            [],
          ),
          h.span(
            [h.Class("grid min-w-0 grow gap-0.5")],
            [
              h.span([], [h.span([h.Class("font-medium")], [agent.name]), ` ${agent.title}`]),
              h.span(
                [h.Class("text-sm text-muted-foreground")],
                [`${bindingText(agent)} · ${detail}`],
              ),
            ],
          ),
          agent.endedAt ? renderBadge(h, "secondary", "stopped") : h.empty,
          state.badge ? renderBadge(h, "outline", state.badge) : h.empty,
        ],
      ),
    ],
  );
}

export const view = defineView<Model, Message>((model, h) => {
  if (Option.isSome(model.transcript)) {
    return h.submodel({
      slotId: "transcript",
      model: model.transcript.value,
      view: Transcript.viewPanel,
      toParentMessage: (message) => Message.GotTranscriptMessage({ message }),
    });
  }
  const stoppedId = `${model.instance}-stopped-agents`;
  return h.div(
    [h.Class("grid gap-2")],
    [
      model.error ? renderBadge(h, "destructive", model.error) : h.empty,
      h.div(
        [h.Class("flex items-center gap-2 px-2")],
        [
          renderSwitch(h, {
            id: stoppedId,
            checked: model.showStopped,
            disabled: false,
            toggle: Message.ToggledStopped(),
          }),
          h.label(
            [
              h.For(stoppedId),
              h.DataAttribute("slot", "label"),
              h.Class("flex items-center gap-2 text-sm leading-none font-medium select-none"),
            ],
            ["Show stopped agents"],
          ),
        ],
      ),
      h.ul(
        [],
        shownAgents(model.agents, model.showStopped).map((agent) => renderEntry(h, agent)),
      ),
    ],
  );
});

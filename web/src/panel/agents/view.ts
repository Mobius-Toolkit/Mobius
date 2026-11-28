import { cn } from "cn";
import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { defineView } from "foldkit/submodel";
import { queueDetail } from "@/lib/agents";
import { bindingText } from "@/lib/harness";
import { waitDot } from "@/lib/state";
import { clock, dayClock, minutesUntil } from "@/lib/time";
import * as Transcript from "@/transcript/view";
import { renderBadge, renderStateBadge } from "@/ui/badge";
import { renderSwitch } from "@/ui/switch";
import { Message } from "./message";
import { shownAgents, type Model } from "./model";
import type { Agent } from "@/transcript/schemas";

type H = HtmlBuilder<Message>;

const endedState = (agent: Agent) => {
  if (!agent.endedAt) {
    return "";
  }
  return agent.keptUntil ? "kept" : "stopped";
};

function renderEntry(h: H, model: Model, agent: Agent): Html {
  const started = `${dayClock(agent.startedAt)}${agent.endedAt ? `–${clock(agent.endedAt)}` : ""}`;
  const reason = queueDetail(agent);
  const queued = reason ? `${started} · ${reason}` : started;
  const detail = agent.keptUntil
    ? `${queued} · closes in ${minutesUntil(agent.keptUntil, model.now)} min`
    : queued;
  const name = `${agent.name.charAt(0).toUpperCase()}${agent.name.slice(1)}`;
  const issue = agent.issue;
  const reference =
    issue === null
      ? ""
      : `${agent.repository === `${model.owner}/${model.name}` ? "" : agent.repository}#${issue}`;
  const label = [
    name,
    agent.title,
    reference,
    agent.issueTitle,
    `${bindingText(agent)} · ${detail}`,
  ]
    .filter((part) => part)
    .join(" ");
  return h.keyed("li")(
    String(agent.id),
    [h.Class("relative grid gap-0.5 rounded-lg p-2 hover:bg-muted")],
    [
      h.button(
        [
          h.Type("button"),
          h.AriaLabel(label),
          h.Class("absolute inset-0 rounded-lg"),
          h.OnClick(Message.OpenedAgent({ agent })),
        ],
        [],
      ),
      h.span(
        [h.Class("flex flex-wrap items-center gap-x-2 gap-y-0.5")],
        [
          h.span(
            [h.Class("flex min-w-0 grow items-start gap-2")],
            [
              h.span(
                [
                  h.Class(
                    cn(
                      "mt-1.5 size-2 shrink-0 rounded-full",
                      agent.endedAt ? "border border-muted-foreground" : waitDot(agent.wait),
                    ),
                  ),
                ],
                [],
              ),
              h.span(
                [h.Class("min-w-0 wrap-anywhere")],
                [
                  h.span([h.Class("font-medium")], [name]),
                  ...(agent.title ? [` ${agent.title}`] : []),
                ],
              ),
            ],
          ),
          renderStateBadge(h, { ...agent, state: endedState(agent), merging: false }),
        ],
      ),
      h.span([h.Class("text-sm text-muted-foreground")], [bindingText(agent)]),
      issue === null
        ? h.empty
        : h.span(
            [h.Class("flex min-w-0 gap-1 text-sm")],
            [
              h.a(
                [
                  h.Href(`https://github.com/${agent.repository}/issues/${issue}`),
                  h.Attribute("target", "_blank"),
                  h.Attribute("rel", "noreferrer"),
                  h.Class("relative z-10 shrink-0 underline"),
                ],
                [reference],
              ),
              ...(agent.issueTitle === null
                ? []
                : [h.span([h.Class("truncate text-muted-foreground")], [agent.issueTitle])]),
            ],
          ),
      h.span([h.Class("text-sm wrap-anywhere text-muted-foreground")], [detail]),
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
        shownAgents(model.agents, model.showStopped).map((agent) => renderEntry(h, model, agent)),
      ),
    ],
  );
});

import { cn } from "cn";
import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { queueState, queueText } from "@/lib/agents";
import { dayClock } from "@/lib/time";
import * as Transcript from "@/transcript/view";
import { renderBadge } from "@/ui/badge";
import { chevronRightIcon } from "@/ui/icons";
import { inset, renderErrorBadge, renderPageHeader } from "@/ui/page";
import { Message } from "./message";
import { titleOf, type Model } from "./model";
import type { ActiveAgent, ActiveAgents } from "./schemas";

type H = HtmlBuilder<Message>;

const rowClass = "flex min-h-11 items-center gap-4 px-4 py-2 md:px-2";

const numbered = (number: number, title?: string | null) =>
  title ? `#${number} ${title}` : `#${number}`;

function renderRow(h: H, row: ActiveAgent): Html {
  const agent = row.agent;
  const state = queueState(agent.queueReason);
  const details = [
    agent.role,
    agent.organization,
    (agent.workstream !== 0 || agent.issue != null) && agent.repository,
    dayClock(agent.startedAt),
    queueText(agent),
  ]
    .filter(Boolean)
    .join(" · ");
  return h.keyed("li")(
    String(agent.id),
    [],
    [
      h.button(
        [
          h.Type("button"),
          h.Class(cn(rowClass, "w-full gap-3 text-left hover:bg-muted")),
          h.OnClick(Message.OpenedAgent({ agent })),
        ],
        [
          h.span([h.Class(`size-2 shrink-0 rounded-full ${state.dot}`)], []),
          h.span(
            [h.Class("grid min-w-0 grow gap-0.5")],
            [
              h.span([], [`${agent.name} ${agent.title}`]),
              h.span([h.Class("text-sm text-muted-foreground")], [details]),
              agent.workstream !== 0
                ? h.span(
                    [h.Class("truncate text-sm text-muted-foreground")],
                    [`Workstream ${numbered(agent.workstream, row.workstreamTitle)}`],
                  )
                : h.empty,
              agent.issue != null
                ? h.span(
                    [h.Class("truncate text-sm text-muted-foreground")],
                    [`Ticket ${numbered(agent.issue, row.issueTitle)}`],
                  )
                : h.empty,
              row.pullRequest != null
                ? h.span(
                    [h.Class("text-sm text-muted-foreground")],
                    [`Pull request #${row.pullRequest}`],
                  )
                : h.empty,
            ],
          ),
          state.badge ? renderBadge(h, "outline", state.badge) : h.empty,
          chevronRightIcon(h, "size-4 shrink-0"),
        ],
      ),
    ],
  );
}

function renderGroups(h: H, agents: ActiveAgents): Html[] {
  return agents.groups.map((group) =>
    h.keyed("section")(
      group.name,
      [h.Class("grid gap-1")],
      [
        h.h3(
          [h.Class(`text-xs font-semibold tracking-wide text-muted-foreground uppercase ${inset}`)],
          [`${group.name} ${group.count} / ${group.max}`],
        ),
        h.ul(
          [h.Class("divide-y md:-mx-2")],
          group.agents.map((row) => renderRow(h, row)),
        ),
      ],
    ),
  );
}

export function view(model: Model, h: H): Html {
  return Option.match(model.transcript, {
    onSome: (transcript) =>
      h.div(
        [h.Class("grid gap-6")],
        [
          renderPageHeader(h, titleOf(transcript), Message.ClickedBack()),
          h.submodel({
            slotId: "transcript",
            model: transcript,
            view: Transcript.viewPage,
            toParentMessage: (message) => Message.GotTranscriptMessage({ message }),
          }),
        ],
      ),
    onNone: () =>
      h.div(
        [h.Class("grid gap-6")],
        [
          renderPageHeader(h, "Agents"),
          model.error ? renderErrorBadge(h, model.error) : h.empty,
          ...Option.match(model.agents, {
            onNone: () => [],
            onSome: (agents) => renderGroups(h, agents),
          }),
        ],
      ),
  });
}

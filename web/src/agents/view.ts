import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { queueDetail } from "@/lib/agents";
import { bindingText } from "@/lib/harness";
import { waitDot } from "@/lib/state";
import { dayClock } from "@/lib/time";
import { transcriptSubtitle, transcriptTitle } from "@/transcript/title";
import * as Transcript from "@/transcript/view";
import { renderStateBadge } from "@/ui/badge";
import { chevronRightIcon } from "@/ui/icons";
import { inset, renderErrorBadge, renderPageHeader } from "@/ui/page";
import { Message } from "./message";
import type { Model } from "./model";
import type { ActiveAgent, ActiveAgents } from "./schemas";

type H = HtmlBuilder<Message>;

const numbered = (number: number, title?: string | null) =>
  title ? `#${number} ${title}` : `#${number}`;

function renderRow(h: H, row: ActiveAgent): Html {
  const agent = row.agent;
  const shownRepository = agent.workstream !== 0 || agent.issue != null;
  const details = [
    !shownRepository && agent.organization,
    shownRepository && agent.repository,
    dayClock(agent.startedAt),
    queueDetail(agent),
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
          h.Class("grid min-h-11 w-full gap-0.5 px-4 py-2 text-left hover:bg-muted md:px-2"),
          h.OnClick(Message.OpenedAgent({ agent })),
        ],
        [
          h.span(
            [h.Class("flex items-start gap-2")],
            [
              h.span(
                [h.Class("flex min-w-0 grow flex-wrap items-center gap-x-2 gap-y-0.5")],
                [
                  h.span(
                    [h.Class("flex min-w-0 grow items-start gap-2")],
                    [
                      h.span(
                        [h.Class(`mt-1.5 size-2 shrink-0 rounded-full ${waitDot(agent.wait)}`)],
                        [],
                      ),
                      h.span([h.Class("min-w-0 wrap-anywhere")], [`${agent.name} ${agent.title}`]),
                    ],
                  ),
                  renderStateBadge(h, { ...agent, state: "" }),
                ],
              ),
              chevronRightIcon(h, "mt-0.5 size-4 shrink-0"),
            ],
          ),
          h.span([h.Class("text-sm text-muted-foreground")], [bindingText(agent)]),
          h.span([h.Class("text-sm wrap-anywhere text-muted-foreground")], [details]),
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
          renderPageHeader(
            h,
            transcriptTitle(transcript.agent),
            Message.ClickedBack(),
            undefined,
            transcriptSubtitle(transcript.agent),
          ),
          h.p(
            [h.Class(`-mb-3 truncate text-sm text-muted-foreground md:hidden ${inset}`)],
            [transcriptSubtitle(transcript.agent)],
          ),
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

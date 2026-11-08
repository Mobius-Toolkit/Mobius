import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { elapsed } from "@/lib/time";
import { renderBadge } from "@/ui/badge";
import { defineRouterLink } from "@/ui/link";
import { linkClass, renderErrorBadge, renderPageHeader } from "@/ui/page";
import { Message } from "./message";
import { columns, type Model } from "./model";
import type { BoardTask } from "./schemas";

type H = HtmlBuilder<Message>;

const RouterLink = defineRouterLink(Message.NavigateTo);

function renderCard(h: H, task: BoardTask, now: number): Html {
  const rounds = [
    task.fixRounds > 0 && `fix ${task.fixRounds}`,
    task.reviewRounds > 0 && `review ${task.reviewRounds}`,
  ].filter(Boolean);
  const path = `/workstreams/${task.workstreamRepository}/${task.workstream}`;
  return h.keyed("li")(
    `${task.repository}#${task.number}@${task.stateAt}`,
    [
      h.Class(
        "relative grid grid-cols-1 gap-1 rounded-lg border bg-card p-3 text-sm hover:bg-muted",
      ),
    ],
    [
      h.a(
        [
          h.Attribute("href", path),
          h.OnMount(RouterLink({ path })),
          h.Class("line-clamp-2 font-medium wrap-anywhere after:absolute after:inset-0"),
        ],
        [task.title],
      ),
      h.span(
        [h.Class("truncate text-muted-foreground")],
        [`${task.repository.split("/")[1]}#${task.number} · ${task.workstreamTitle}`],
      ),
      h.span(
        [h.Class("flex flex-wrap items-center gap-x-2 gap-y-1")],
        [
          renderBadge(h, "secondary", task.merging ? "approved" : task.state),
          task.pullRequest !== null && task.pullRequestUrl !== null
            ? h.a(
                [
                  h.Attribute("href", task.pullRequestUrl),
                  h.Attribute("target", "_blank"),
                  h.Attribute("rel", "noreferrer"),
                  h.Class(`relative z-10 whitespace-nowrap ${linkClass}`),
                ],
                [`PR #${task.pullRequest}`],
              )
            : h.empty,
          h.span(
            [h.Class("ml-auto whitespace-nowrap text-muted-foreground")],
            [elapsed(task.stateAt, now)],
          ),
        ],
      ),
      task.mergeRefusal
        ? h.span([h.Class("wrap-anywhere text-muted-foreground")], [task.mergeRefusal])
        : h.empty,
      rounds.length > 0
        ? h.span([h.Class("text-muted-foreground")], [rounds.join(" · ")])
        : h.empty,
    ],
  );
}

function renderColumn(h: H, title: string, tasks: readonly BoardTask[], now: number): Html {
  const cards = tasks.toSorted((a, b) =>
    title === "Done"
      ? Date.parse(b.stateAt) - Date.parse(a.stateAt)
      : Date.parse(a.stateAt) - Date.parse(b.stateAt),
  );
  return h.keyed("section")(
    title,
    [
      h.AriaLabel(title),
      h.Class("flex min-h-0 w-[85vw] shrink-0 snap-start flex-col gap-2 md:w-0 md:flex-1"),
    ],
    [
      h.h3(
        [
          h.Class(
            "flex items-center gap-2 text-xs font-semibold tracking-wide text-muted-foreground uppercase",
          ),
        ],
        [title, renderBadge(h, "secondary", String(cards.length))],
      ),
      cards.length === 0 ? h.p([h.Class("text-sm text-muted-foreground")], ["No tasks"]) : h.empty,
      h.ul(
        [h.Class("grid min-h-0 grid-cols-1 content-start gap-2 overflow-y-auto")],
        cards.map((task) => renderCard(h, task, now)),
      ),
    ],
  );
}

export function view(model: Model, h: H): Html {
  const shown = Option.map(model.tasks, (list) =>
    list.filter((task) => task.workstreamRepository.startsWith(`${model.organization}/`)),
  );
  return h.div(
    [h.Class("flex min-h-0 min-w-0 grow flex-col gap-2 pt-4")],
    [
      h.div([h.Class("px-4")], [renderPageHeader(h, "Tasks")]),
      model.error ? renderErrorBadge(h, model.error) : h.empty,
      Option.isSome(shown)
        ? h.div(
            [
              h.Class(
                "flex min-h-0 grow snap-x snap-mandatory scroll-px-4 gap-3 overflow-x-auto px-4 pb-4 md:snap-none",
              ),
            ],
            columns.map(({ title, has }) =>
              renderColumn(h, title, shown.value.filter(has), model.now),
            ),
          )
        : h.empty,
    ],
  );
}

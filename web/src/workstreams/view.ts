import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import { levelName } from "@/lib/autopilot";
import { unreadCount } from "@/lib/unread";
import { renderBadge } from "@/ui/badge";
import { autopilotIcon, chevronRightIcon, gitPullRequestArrowIcon } from "@/ui/icons";
import { defineRouterLink } from "@/ui/link";
import { inset, renderErrorBadge, renderPageHeader } from "@/ui/page";
import { Message } from "./message";
import type { Model } from "./model";
import type { Workstream } from "./schemas";

type H = HtmlBuilder<Message>;

const RouterLink = defineRouterLink(Message.NavigateTo);

function renderBadges(h: H, model: Model, workstream: Workstream): Html {
  const needsHuman = model.needsHuman.some(
    (issue) => issue.repository === workstream.repository && issue.workstream === workstream.number,
  );
  const count = unreadCount(model.unread, {
    organization: workstream.repository.split("/")[0],
    repository: workstream.repository,
    workstream: workstream.number,
  });
  const autopilot = `Autopilot: ${levelName(workstream.autopilot)}`;
  return h.span(
    [h.Class("flex shrink-0 items-center gap-1.5")],
    [
      model.working.includes(`${workstream.repository}#${workstream.number}`)
        ? h.span(
            [
              h.Role("img"),
              h.AriaLabel("Agent running"),
              h.Attribute("title", "Agent running"),
              h.Class("size-2 rounded-full bg-green-600"),
            ],
            [],
          )
        : h.empty,
      workstream.autopilot !== "off"
        ? h.span(
            [h.Role("img"), h.AriaLabel(autopilot), h.Attribute("title", autopilot)],
            [autopilotIcon(h, workstream.autopilot)],
          )
        : h.empty,
      workstream.readyToMerge
        ? h.span(
            [
              h.Role("img"),
              h.AriaLabel("Ready to merge"),
              h.Attribute("title", "Ready to merge"),
              h.Class("text-green-600 dark:text-green-500"),
            ],
            [gitPullRequestArrowIcon(h)],
          )
        : h.empty,
      h.span([h.Class("text-muted-foreground")], [`#${workstream.number}`]),
      workstream.allTasksClosed && !workstream.evergreen
        ? renderBadge(h, "secondary", "done")
        : h.empty,
      needsHuman
        ? renderBadge(
            h,
            "default",
            "needs you",
            "bg-amber-500/15 text-amber-700 dark:text-amber-400",
          )
        : h.empty,
      count > 0 ? renderBadge(h, "default", String(count)) : h.empty,
    ],
  );
}

function renderRow(h: H, model: Model, workstream: Workstream): Html {
  const path = `/workstreams/${workstream.repository}/${workstream.number}`;
  return h.keyed("li")(
    `${workstream.repository}#${workstream.number}`,
    [],
    [
      h.a(
        [
          h.Attribute("href", path),
          h.OnMount(RouterLink({ path })),
          h.Class("flex min-h-11 items-center gap-4 px-4 py-2 hover:bg-muted md:px-2"),
        ],
        [
          h.span([h.Class("min-w-0 grow break-words")], [workstream.title]),
          renderBadges(h, model, workstream),
          chevronRightIcon(h, "size-4 shrink-0"),
        ],
      ),
    ],
  );
}

function renderList(h: H, model: Model, workstreams: readonly Workstream[]): Html {
  return h.ul(
    [h.Class("divide-y md:-mx-2")],
    workstreams.map((workstream) => renderRow(h, model, workstream)),
  );
}

export function view(model: Model, h: H): Html {
  const shown = Option.map(model.workstreams, (list) =>
    list.filter((workstream) => workstream.repository.startsWith(`${model.organization}/`)),
  );
  const evergreen = Option.getOrElse(shown, () => []).filter((workstream) => workstream.evergreen);
  const others = Option.getOrElse(shown, () => []).filter((workstream) => !workstream.evergreen);
  return h.div(
    [h.Class("grid gap-6")],
    [
      renderPageHeader(h, "Workstreams"),
      model.error ? renderErrorBadge(h, model.error) : h.empty,
      Option.exists(shown, (list) => list.length === 0)
        ? h.p(
            [h.Class(`text-muted-foreground ${inset}`)],
            ["This organization has no open Workstream."],
          )
        : h.empty,
      evergreen.length > 0
        ? h.section(
            [h.Class("grid gap-1")],
            [
              h.h3(
                [
                  h.Class(
                    `text-xs font-semibold tracking-wide text-muted-foreground uppercase ${inset}`,
                  ),
                ],
                ["Evergreen"],
              ),
              renderList(h, model, evergreen),
            ],
          )
        : h.empty,
      others.length > 0 ? renderList(h, model, others) : h.empty,
    ],
  );
}

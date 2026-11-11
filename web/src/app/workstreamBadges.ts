import type { Html, HtmlBuilder } from "foldkit/html";
import type { Unread, Workstream } from "@/api/api.gen";
import { levelName } from "@/lib/autopilot";
import { unreadCount } from "@/lib/unread";
import { workstreamKey, type WorkstreamState as WorkstreamLists } from "@/lib/workstreams";
import { renderBadge } from "@/ui/badge";
import { autopilotIcon, gitPullRequestArrowIcon } from "@/ui/icons";

export function renderWorkstreamBadges<M>(
  h: HtmlBuilder<M>,
  workstream: Workstream,
  workstreams: WorkstreamLists,
  unread: readonly Unread[],
): Html {
  const needsHuman = workstreams.needsHuman.some(
    (issue) => issue.repository === workstream.repository && issue.workstream === workstream.number,
  );
  const count = unreadCount(unread, {
    organization: workstream.repository.split("/")[0],
    repository: workstream.repository,
    workstream: workstream.number,
  });

  const items: Html[] = [];

  if (workstreams.working.has(workstreamKey(workstream))) {
    items.push(
      h.span(
        [
          h.Role("img"),
          h.AriaLabel("Agent running"),
          h.Title("Agent running"),
          h.Class("size-2 rounded-full bg-green-600"),
        ],
        [],
      ),
    );
  }

  if (workstream.autopilot !== "off") {
    const label = `Autopilot: ${levelName(workstream.autopilot)}`;
    items.push(
      h.span(
        [h.Role("img"), h.AriaLabel(label), h.Title(label)],
        [autopilotIcon(h, workstream.autopilot)],
      ),
    );
  }

  if (workstream.readyToMerge) {
    items.push(
      h.span(
        [
          h.Role("img"),
          h.AriaLabel("Ready to merge"),
          h.Title("Ready to merge"),
          h.Class("text-green-600 dark:text-green-500"),
        ],
        [gitPullRequestArrowIcon(h, "size-3.5")],
      ),
    );
  }

  items.push(h.span([h.Class("text-muted-foreground")], [`#${workstream.number}`]));

  if (workstream.allTasksClosed && !workstream.evergreen) {
    items.push(renderBadge(h, "secondary", "done"));
  }

  if (needsHuman) {
    items.push(
      renderBadge(h, "default", "needs you", "bg-amber-500/15 text-amber-700 dark:text-amber-400"),
    );
  }

  if (count > 0) {
    items.push(renderBadge(h, "default", String(count)));
  }

  return h.span([h.Class("flex shrink-0 items-center gap-1.5")], items);
}

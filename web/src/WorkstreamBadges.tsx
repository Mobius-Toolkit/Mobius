import { GitPullRequestArrowIcon } from "lucide-react";
import type { Unread, Workstream } from "@/api/api.gen";
import { Badge } from "@/components/ui/badge";
import { levelName } from "@/lib/autopilot";
import { unreadCount } from "@/lib/unread";
import { workstreamKey, type Workstreams as WorkstreamLists } from "@/lib/workstreams";
import { AutopilotIcon } from "./Autopilot";

export function WorkstreamBadges({
  workstream,
  workstreams,
  unread,
}: {
  workstream: Workstream;
  workstreams: WorkstreamLists;
  unread: Unread[];
}) {
  const needsHuman = workstreams.needsHuman.some(
    (issue) => issue.repository === workstream.repository && issue.workstream === workstream.number,
  );
  const count = unreadCount(unread, {
    organization: workstream.repository.split("/")[0],
    repository: workstream.repository,
    workstream: workstream.number,
  });
  return (
    <span className="flex shrink-0 items-center gap-1.5">
      {workstreams.working.has(workstreamKey(workstream)) && (
        <span
          role="img"
          aria-label="Agent running"
          title="Agent running"
          className="size-2 rounded-full bg-green-600"
        />
      )}
      {workstream.autopilot !== "off" && (
        <span
          role="img"
          aria-label={`Autopilot: ${levelName(workstream.autopilot)}`}
          title={`Autopilot: ${levelName(workstream.autopilot)}`}
        >
          <AutopilotIcon level={workstream.autopilot} />
        </span>
      )}
      {workstream.readyToMerge && (
        <span
          role="img"
          aria-label="Ready to merge"
          title="Ready to merge"
          className="text-green-600 dark:text-green-500"
        >
          <GitPullRequestArrowIcon className="size-3.5" />
        </span>
      )}
      <span className="text-muted-foreground">#{workstream.number}</span>
      {workstream.allTasksClosed && !workstream.evergreen && (
        <Badge variant="secondary">done</Badge>
      )}
      {needsHuman && (
        <Badge className="bg-amber-500/15 text-amber-700 dark:text-amber-400">needs you</Badge>
      )}
      {count > 0 && <Badge>{count}</Badge>}
    </span>
  );
}

import { GitPullRequestArrowIcon, PlaneIcon } from "lucide-react";
import type { Unread, Workstream } from "@/api/api.gen";
import { Badge } from "@/components/ui/badge";
import { ErrorBadge, inset, LinkRow, List, PageHeader } from "@/components/page";
import { unreadCount } from "@/lib/unread";
import { cn } from "@/lib/utils";
import {
  chatParams,
  organizationWorkstreams,
  workstreamKey,
  type Workstreams as WorkstreamLists,
} from "@/lib/workstreams";
import { TopBar } from "./TopBar";

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
      {workstream.autopilot && (
        <span role="img" aria-label="Autopilot" title="Autopilot" className="text-muted-foreground">
          <PlaneIcon className="size-3.5" />
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
      {workstream.allTasksClosed && <Badge variant="secondary">done</Badge>}
      {needsHuman && (
        <Badge className="bg-amber-500/15 text-amber-700 dark:text-amber-400">needs you</Badge>
      )}
      {count > 0 && <Badge>{count}</Badge>}
    </span>
  );
}

export function Workstreams({
  organization,
  workstreams,
  unread,
}: {
  organization: string;
  workstreams: WorkstreamLists;
  unread: Unread[];
}) {
  const shown = organizationWorkstreams(workstreams, organization);
  return (
    <>
      <TopBar title="Workstreams" />
      <PageHeader title="Workstreams" />
      {workstreams.error && <ErrorBadge>{workstreams.error}</ErrorBadge>}
      {shown?.length === 0 && (
        <p className={cn("text-muted-foreground", inset)}>
          This organization has no open Workstream.
        </p>
      )}
      <List>
        {shown?.map((workstream) => (
          <LinkRow
            key={`${workstream.repository}#${workstream.number}`}
            link={{ to: "/workstreams/$owner/$name/$number", params: chatParams(workstream) }}
            title={workstream.title}
          >
            <WorkstreamBadges workstream={workstream} workstreams={workstreams} unread={unread} />
          </LinkRow>
        ))}
      </List>
    </>
  );
}

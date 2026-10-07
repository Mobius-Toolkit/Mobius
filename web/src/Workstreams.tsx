import { Link } from "@tanstack/react-router";
import type { Unread, Workstream } from "@/api/api.gen";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { unreadCount } from "@/lib/unread";
import {
  chatParams,
  organizationWorkstreams,
  type Workstreams as WorkstreamLists,
} from "@/lib/workstreams";

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
    <Card>
      <CardHeader>
        <CardTitle>Workstreams</CardTitle>
      </CardHeader>
      <CardContent>
        {workstreams.error && <Badge variant="destructive">{workstreams.error}</Badge>}
        {shown?.length === 0 && (
          <p className="text-muted-foreground">This organization has no open Workstream.</p>
        )}
        <ul className="divide-y">
          {shown?.map((workstream) => (
            <li key={`${workstream.repository}#${workstream.number}`}>
              <Link
                to="/workstreams/$owner/$name/$number"
                params={chatParams(workstream)}
                className="-mx-2 flex items-center justify-between gap-4 rounded-md px-2 py-2 hover:bg-muted"
              >
                <span>{workstream.title}</span>
                <WorkstreamBadges
                  workstream={workstream}
                  workstreams={workstreams}
                  unread={unread}
                />
              </Link>
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  );
}

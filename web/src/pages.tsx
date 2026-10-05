import { useParams } from "@tanstack/react-router";
import { useShell } from "@/lib/shell";
import { unreadCount } from "@/lib/unread";
import { Activity } from "./Activity";
import { Agents } from "./Agents";
import { Chat } from "./Chat";
import { Checkup } from "./Checkup";
import { GitHub } from "./GitHub";
import { Inbox } from "./Inbox";
import { NewWorkstream } from "./NewWorkstream";
import { Workstreams } from "./Workstreams";

export function WorkstreamsPage() {
  const { organization, workstreams, unread } = useShell();
  return (
    <Workstreams organization={organization} workstreams={workstreams} unread={unread ?? []} />
  );
}

export function NewWorkstreamPage() {
  const { organizations, organization, unread, source } = useShell();
  return (
    <NewWorkstream
      organizations={organizations}
      organization={organization}
      unread={unread && unreadCount(unread, { organization, repository: "", workstream: 0 })}
      source={source}
    />
  );
}

export function ChatPage() {
  const { owner, name, number } = useParams({
    from: "/workstreams/$owner/$name/$number",
  });
  const { workstreams, unread, source } = useShell();
  return (
    <Chat
      key={`${owner}/${name}#${number}`}
      owner={owner}
      name={name}
      number={Number(number)}
      workstreams={workstreams}
      unread={
        unread &&
        unreadCount(unread, {
          organization: owner,
          repository: `${owner}/${name}`,
          workstream: Number(number),
        })
      }
      source={source}
    />
  );
}

export function InboxPage() {
  const { organization, inbox, workstreams } = useShell();
  return <Inbox organization={organization} items={inbox} workstreams={workstreams.list} />;
}

export function ActivityPage() {
  const { organization, activities, workstreams } = useShell();
  return (
    <Activity organization={organization} activities={activities} workstreams={workstreams.list} />
  );
}

export function AgentsPage() {
  const { source } = useShell();
  return <Agents source={source} />;
}

export function CheckupPage() {
  const { organization } = useShell();
  return <Checkup organization={organization} />;
}

export function GitHubPage() {
  const { apps } = useShell();
  return <GitHub apps={apps} back />;
}

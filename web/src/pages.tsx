import { useParams } from "@tanstack/react-router";
import { useShell } from "@/lib/shell";
import { unreadCount } from "@/lib/unread";
import { Activity } from "./Activity";
import { Agents } from "./Agents";
import { Chat } from "./Chat";
import { Checkup, CheckupLabels, CheckupPermissions, CheckupTools } from "./Checkup";
import { GitHub } from "./GitHub";
import { Inbox } from "./Inbox";
import { InboxTabs } from "./InboxTabs";
import { TriagerChat } from "./TriagerChat";
import { Workstreams } from "./Workstreams";

export function WorkstreamsPage() {
  const { organization, workstreams, unread } = useShell();
  return (
    <Workstreams organization={organization} workstreams={workstreams} unread={unread ?? []} />
  );
}

export function TriagerChatPage() {
  const { organizations, organization, unread, source } = useShell();
  return (
    <TriagerChat
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

export function InboxLayoutPage() {
  const { organization, inbox } = useShell();
  return <InboxTabs count={inbox.filter((item) => item.organization === organization).length} />;
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
  const { organizations } = useShell();
  return <Checkup organizations={organizations} />;
}

export function CheckupToolsPage() {
  return <CheckupTools />;
}

export function CheckupPermissionsPage() {
  const { organization } = useParams({ from: "/settings/checkup/$organization/permissions" });
  return <CheckupPermissions key={organization} organization={organization} />;
}

export function CheckupLabelsPage() {
  const { organization } = useParams({ from: "/settings/checkup/$organization/labels" });
  return <CheckupLabels key={organization} organization={organization} />;
}

export function GitHubPage() {
  const { apps } = useShell();
  return <GitHub apps={apps} back />;
}

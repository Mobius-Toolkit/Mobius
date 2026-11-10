import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { PageHeader } from "@/components/page";
import { useShell } from "@/lib/shell";
import { Agents } from "./Agents";
import { Chat } from "./Chat";
import { CheckupHost } from "./CheckupHost";
import { GitHub } from "./GitHub";
import { Inbox } from "./Inbox";
import { Memory, MemoryFiles, MemoryRepositories } from "./Memory";
import { Tasks } from "./Tasks";
import { TopBar } from "./TopBar";
import { CoordinatorChat } from "./CoordinatorChat";
import { Usage } from "./Usage";
import { Workstreams } from "./Workstreams";

export function WorkstreamsPage() {
  const { organization, workstreams } = useShell();
  return (
    <>
      <TopBar title="Workstreams" />
      <Workstreams organization={organization} needsHuman={workstreams.needsHuman} />
    </>
  );
}

export function TasksPage() {
  const { organization } = useShell();
  return (
    <>
      <TopBar title="Tasks" />
      <Tasks organization={organization} />
    </>
  );
}

export function CoordinatorChatPage() {
  const { organizations, organization, inbox } = useShell();
  return (
    <CoordinatorChat organizations={organizations} organization={organization} inbox={inbox} />
  );
}

export function ChatPage() {
  const { owner, name, number } = useParams({
    from: "/workstreams/$owner/$name/$number",
  });
  const { workstreams, inbox } = useShell();
  return (
    <Chat
      key={`${owner}/${name}#${number}`}
      owner={owner}
      name={name}
      number={Number(number)}
      workstreams={workstreams}
      inbox={inbox}
    />
  );
}

export function InboxPage() {
  const { organization, inbox, selectOrganization } = useShell();
  return (
    <>
      <TopBar title="Inbox" />
      <PageHeader title="Inbox" />
      <Inbox organization={organization} items={inbox} selectOrganization={selectOrganization} />
    </>
  );
}

export function AgentsPage() {
  return <Agents />;
}

export function CheckupPage() {
  return <CheckupHost page="overview" title="Checkup" back="/settings" />;
}

export function CheckupToolsPage() {
  return <CheckupHost page="tools" title="Tools" back="/settings/checkup" />;
}

export function CheckupPermissionsPage() {
  const { organization } = useParams({ from: "/settings/checkup/$organization/permissions" });
  return (
    <CheckupHost
      key={organization}
      page="permissions"
      organization={organization}
      title="App permissions"
      back="/settings/checkup"
    />
  );
}

export function CheckupLabelsPage() {
  const { organization } = useParams({ from: "/settings/checkup/$organization/labels" });
  return (
    <CheckupHost
      key={organization}
      page="labels"
      organization={organization}
      title="Labels"
      back="/settings/checkup"
    />
  );
}

export function CheckupWorkstreamsPage() {
  const { organization } = useParams({ from: "/settings/checkup/$organization/workstreams" });
  return (
    <CheckupHost
      key={organization}
      page="workstreams"
      organization={organization}
      title="Workstreams"
      back="/settings/checkup"
    />
  );
}

export function GitHubPage() {
  const { apps } = useShell();
  return <GitHub apps={apps} back />;
}

export function MemoryRepositoriesPage() {
  return <MemoryRepositories />;
}

export function MemoryPage() {
  const { owner, name } = useParams({ from: "/settings/memory/$owner/$name" });
  return <MemoryFiles key={`${owner}/${name}`} owner={owner} name={name} />;
}

export function MemoryFilePage() {
  const { owner, name, file } = useParams({ from: "/settings/memory/$owner/$name/$file" });
  return <Memory key={`${owner}/${name}/${file}`} owner={owner} name={name} file={file} />;
}

export function UsagePage() {
  const search = useSearch({ from: "/usage" });
  const navigate = useNavigate({ from: "/usage" });
  return (
    <Usage
      search={search}
      onSearch={(patch) => navigate({ search: (prev) => ({ ...prev, ...patch }), replace: true })}
    />
  );
}

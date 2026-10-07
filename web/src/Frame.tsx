import { Link, useMatchRoute, type LinkProps } from "@tanstack/react-router";
import { InboxIcon, LayersIcon, ListIcon, PlusIcon, SettingsIcon } from "lucide-react";
import type { ReactNode } from "react";
import type { InboxItem, Unread } from "@/api/api.gen";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useNewBuild } from "@/lib/build";
import { settingsPages } from "@/lib/settings";
import { useUpgrade } from "@/lib/upgrade";
import { cn } from "@/lib/utils";
import { chatParams, organizationWorkstreams, type Workstreams } from "@/lib/workstreams";
import { OrganizationSwitch } from "./OrganizationSwitch";
import { UpgradeControls, UpgradeDialog } from "./Upgrade";
import { WorkstreamBadges } from "./Workstreams";

const tabs = [
  { path: "/workstreams", title: "Workstreams", Icon: LayersIcon },
  { path: "/inbox", title: "Inbox", Icon: InboxIcon },
  { path: "/activity", title: "Activity", Icon: ListIcon },
  { path: "/settings", title: "Settings", Icon: SettingsIcon },
];

function SideLink({
  link,
  fuzzy,
  className,
  children,
}: {
  link: LinkProps;
  fuzzy?: boolean;
  className?: string;
  children: ReactNode;
}) {
  const matchRoute = useMatchRoute();
  return (
    <Button
      asChild
      variant={matchRoute({ ...link, fuzzy }) ? "secondary" : "ghost"}
      className={cn("justify-start", className)}
    >
      <Link {...link} activeOptions={{ exact: !fuzzy }}>
        {children}
      </Link>
    </Button>
  );
}

// fill gives the page the full height below the header, so that the page scrolls its own parts.
export function Frame({
  path,
  organizations,
  organization,
  onSelect,
  source,
  workstreams,
  unread,
  inbox,
  fill,
  children,
}: {
  path: string;
  organizations: string[];
  organization: string;
  onSelect: (organization: string) => void;
  source?: EventSource;
  workstreams: Workstreams;
  unread: Unread[];
  inbox: InboxItem[];
  fill: boolean;
  children: ReactNode;
}) {
  const upgrade = useUpgrade(source);
  const newBuild = useNewBuild();
  const counts = new Map<string, number>();
  for (const item of inbox) {
    counts.set(item.organization, (counts.get(item.organization) ?? 0) + 1);
  }
  for (const chat of unread) {
    counts.set(chat.organization, (counts.get(chat.organization) ?? 0) + chat.count);
  }
  const inboxCount = inbox.filter((item) => item.organization === organization).length;
  const organizationSwitch = organizations.length > 1 && (
    <OrganizationSwitch
      organizations={organizations}
      organization={organization}
      counts={counts}
      onSelect={onSelect}
    />
  );
  const upgradeControls = <UpgradeControls upgrade={upgrade} newBuild={newBuild} />;
  const currentTab = path.startsWith("/workstreams") ? "/workstreams" : path;
  return (
    <div className={cn("flex", fill ? "h-svh" : "min-h-svh")}>
      <nav className="sticky top-0 hidden h-svh w-52 shrink-0 flex-col gap-1 overflow-y-auto border-r bg-sidebar p-2 text-sidebar-foreground md:flex">
        <div className="px-2 py-1">
          {organizationSwitch || <span className="font-semibold">Mobius</span>}
        </div>
        <SideLink link={{ to: "/inbox" }}>
          <span className="grow">Inbox</span>
          {inboxCount > 0 && <Badge>{inboxCount}</Badge>}
        </SideLink>
        <SideLink link={{ to: "/activity" }}>Activity</SideLink>
        <SideLink link={{ to: "/workstreams" }}>Workstreams</SideLink>
        {organizationWorkstreams(workstreams, organization)?.map((workstream) => (
          <SideLink
            key={`${workstream.repository}#${workstream.number}`}
            link={{
              to: "/workstreams/$owner/$name/$number",
              params: chatParams(workstream),
            }}
            className="h-auto min-h-8 flex-wrap py-1.5 pl-5 whitespace-normal"
          >
            <span className="grow">{workstream.title}</span>
            <WorkstreamBadges workstream={workstream} workstreams={workstreams} unread={unread} />
          </SideLink>
        ))}
        <SideLink link={{ to: "/workstreams/new" }} className="pl-5">
          <PlusIcon />
          New Workstream
        </SideLink>
        <div className="grow" />
        {upgradeControls}
        {settingsPages.map((page) => (
          <SideLink key={page.path} link={{ to: page.path }} fuzzy>
            {page.title}
          </SideLink>
        ))}
      </nav>
      <div className="flex min-w-0 grow flex-col pb-20 md:pb-0">
        <header className="grid gap-2 px-4 pt-4 empty:hidden md:hidden">
          {organizationSwitch}
          {upgradeControls}
        </header>
        <main
          className={cn(
            fill
              ? "flex min-h-0 grow"
              : "mx-auto grid w-full max-w-3xl content-start gap-6 p-4 md:p-6",
          )}
        >
          {children}
        </main>
      </div>
      <nav className="fixed inset-x-0 bottom-0 flex border-t bg-sidebar pb-[env(safe-area-inset-bottom)] md:hidden">
        {tabs.map(({ path: tabPath, title, Icon }) => (
          <Link
            key={tabPath}
            to={tabPath}
            activeOptions={{ exact: true }}
            aria-current={currentTab === tabPath ? "page" : undefined}
            className={cn(
              "flex flex-1 flex-col items-center gap-1 py-2 text-xs",
              currentTab === tabPath ? "font-medium text-foreground" : "text-muted-foreground",
            )}
          >
            <Icon className="size-5" />
            <span className="flex items-center gap-1">
              {title}
              {tabPath === "/inbox" && inboxCount > 0 && <Badge>{inboxCount}</Badge>}
            </span>
          </Link>
        ))}
      </nav>
      <UpgradeDialog upgrade={upgrade} />
    </div>
  );
}

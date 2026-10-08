import { Link, useMatchRoute, type LinkProps } from "@tanstack/react-router";
import { ArrowUpIcon, InboxIcon, LayersIcon, MessageCircleIcon, SettingsIcon } from "lucide-react";
import { useState, type ReactNode } from "react";
import type { InboxItem, Unread } from "@/api/api.gen";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useNewBuild } from "@/lib/build";
import { settingsPages } from "@/lib/settings";
import { TopBarContext } from "@/lib/topbar";
import { unreadCount } from "@/lib/unread";
import { hasUpgradeControls, useUpgrade } from "@/lib/upgrade";
import { cn } from "@/lib/utils";
import { chatParams, organizationWorkstreams, type Workstreams } from "@/lib/workstreams";
import { OrganizationSwitch } from "./OrganizationSwitch";
import { UpgradeControls, UpgradeDialog } from "./Upgrade";
import { WorkstreamBadges } from "./Workstreams";

const tabs = [
  { path: "/chat", title: "Chat", Icon: MessageCircleIcon },
  { path: "/workstreams", title: "Workstreams", Icon: LayersIcon },
  { path: "/inbox", title: "Inbox", Icon: InboxIcon },
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

// fill gives the page the full height of the content area, so that the page scrolls its own parts.
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
  const [topBar, setTopBar] = useState<HTMLElement | null>(null);
  const counts = new Map<string, number>();
  for (const item of inbox) {
    counts.set(item.organization, (counts.get(item.organization) ?? 0) + 1);
  }
  for (const chat of unread) {
    counts.set(chat.organization, (counts.get(chat.organization) ?? 0) + chat.count);
  }
  const inboxCount = inbox.filter((item) => item.organization === organization).length;
  const chatCount = unreadCount(unread, { organization, repository: "", workstream: 0 });
  const organizationSwitch = organizations.length > 1 && (
    <OrganizationSwitch
      organizations={organizations}
      organization={organization}
      counts={counts}
      onSelect={onSelect}
    />
  );
  const currentTab = ["/workstreams", "/inbox"].find((tab) => path.startsWith(tab)) ?? path;
  const tabBadges: Record<string, ReactNode> = {
    "/chat": chatCount > 0 && <Badge>{chatCount}</Badge>,
    "/inbox": inboxCount > 0 && <Badge>{inboxCount}</Badge>,
    "/settings": hasUpgradeControls(upgrade, newBuild) && (
      <Badge aria-label="Upgrade available">
        <ArrowUpIcon />
      </Badge>
    ),
  };
  return (
    <div className={cn("flex h-svh flex-col md:flex-row", !fill && "md:h-auto md:min-h-svh")}>
      <nav className="sticky top-0 hidden h-svh w-52 shrink-0 flex-col gap-1 overflow-y-auto border-r bg-sidebar p-2 text-sidebar-foreground md:flex">
        <div className="px-2 py-1">
          {organizationSwitch || <span className="font-semibold">Mobius</span>}
        </div>
        <SideLink link={{ to: "/chat" }}>
          <span className="grow">Chat</span>
          {chatCount > 0 && <Badge>{chatCount}</Badge>}
        </SideLink>
        <SideLink link={{ to: "/inbox" }} fuzzy>
          <span className="grow">Inbox</span>
          {inboxCount > 0 && <Badge>{inboxCount}</Badge>}
        </SideLink>
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
        <div className="grow" />
        <UpgradeControls upgrade={upgrade} newBuild={newBuild} />
        {settingsPages.map((page) => (
          <SideLink key={page.path} link={{ to: page.path }}>
            {page.title}
          </SideLink>
        ))}
      </nav>
      <div className="flex min-h-0 min-w-0 grow flex-col">
        <header className="shrink-0 border-b pt-[env(safe-area-inset-top)] md:hidden">
          <div ref={setTopBar} className="flex min-h-14 items-center gap-2 px-2" />
        </header>
        <div
          id="content"
          className="flex min-h-0 grow flex-col overflow-y-auto md:overflow-visible"
        >
          <div className="grid gap-2 px-4 pt-4 empty:hidden md:hidden">
            {(path === "/chat" ||
              path === "/workstreams" ||
              path.startsWith("/inbox") ||
              path === "/settings/memory") &&
              organizationSwitch}
            {path === "/settings" && <UpgradeControls upgrade={upgrade} newBuild={newBuild} />}
          </div>
          <main
            className={cn(
              fill ? "flex min-h-0 grow" : "grid w-full content-start gap-6 py-4 md:p-6",
            )}
          >
            <TopBarContext value={topBar}>{children}</TopBarContext>
          </main>
        </div>
      </div>
      <nav className="flex shrink-0 border-t bg-sidebar pb-[env(safe-area-inset-bottom)] md:hidden">
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
              {tabBadges[tabPath]}
            </span>
          </Link>
        ))}
      </nav>
      <UpgradeDialog upgrade={upgrade} />
    </div>
  );
}

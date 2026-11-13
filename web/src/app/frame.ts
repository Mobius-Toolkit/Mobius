import { cn } from "cn";
import { Option } from "effect";
import type { Html, HtmlBuilder } from "foldkit/html";
import type { Workstream } from "@/api/api.gen";
import { filterNeedsHuman } from "@/lib/inbox";
import { settingsPages } from "@/lib/settings";
import { unreadCount } from "@/lib/unread";
import { Message as AgentsMessage } from "@/agents/message";
import { titleOf as agentTitle } from "@/agents/model";
import { Message as CheckupMsg } from "@/checkup/message";
import { fixButtonOf } from "@/checkup/model";
import {
  chatParams,
  organizationWorkstreams,
  type WorkstreamState as WorkstreamLists,
} from "@/lib/workstreams";
import { badgeClass, renderBadge } from "@/ui/badge";
import { buttonClass, renderButton } from "@/ui/button";
import { renderConnectionBadge } from "@/ui/connection";
import {
  arrowLeftIcon,
  arrowUpIcon,
  checkIcon,
  fileTextIcon,
  inboxIcon,
  kanbanIcon,
  layersIcon,
  messageCircleIcon,
  settingsIcon,
} from "@/ui/icons";
import { pageTitle } from "@/settings/memory/model";
import { renderAutopilotSheet, shownAutopilot } from "./autopilot";
import { renderCIStops } from "./ciStops";
import { Message } from "./message";
import type { Model } from "./model";
import { renderOrganizationSwitch } from "./organizationSwitch";
import { renderPauses } from "./pauses";
import { isFillRoute, parentPath, routeToPath, AppRoute } from "./route";
import { renderSwitchLines } from "./switchLines";
import { renderUpgradeControls, renderUpgradeDialog } from "./upgrade";
import { renderWorkstreamBadges } from "./workstreamBadges";

const tabs = [
  { path: "/chat", title: "Chat", icon: messageCircleIcon },
  { path: "/workstreams", title: "Workstreams", icon: layersIcon },
  { path: "/tasks", title: "Tasks", icon: kanbanIcon },
  { path: "/inbox", title: "Inbox", icon: inboxIcon },
  { path: "/settings", title: "Settings", icon: settingsIcon },
] as const;

function hasUpgradeControls(model: Model): boolean {
  return Boolean(
    model.upgrade.step !== "idle" ||
    (Option.isSome(model.upgrade.drain) && model.upgrade.drain.value.on) ||
    model.upgrade.version ||
    model.upgrade.failure ||
    model.newBuild,
  );
}

function renderNotice(h: HtmlBuilder<Message>, text: string): Html {
  return h.div(
    [
      h.Role("status"),
      h.DataAttribute("slot", "notice"),
      h.Class(
        "flex max-w-md items-start gap-2 rounded-xl bg-card px-3 py-2 text-sm text-card-foreground shadow-md ring-1 ring-foreground/10",
      ),
    ],
    [
      checkIcon(h, "mt-0.5 size-4 shrink-0 text-green-600 dark:text-green-400"),
      h.span([h.Class("line-clamp-2 min-w-0 wrap-anywhere")], [text]),
    ],
  );
}

function renderSideLink(
  h: HtmlBuilder<Message>,
  options: {
    path: string;
    currentPath: string;
    fuzzy?: boolean;
    className?: string;
    children: ReadonlyArray<Html | string>;
  },
): Html {
  const isActive = options.fuzzy
    ? options.currentPath.startsWith(options.path)
    : options.currentPath === options.path;

  return h.a(
    [
      h.Attribute("href", options.path),
      ...(isActive ? [h.AriaCurrent("page")] : []),
      h.Class(
        buttonClass({
          variant: isActive ? "secondary" : "ghost",
          className: cn("justify-start", options.className),
        }),
      ),
    ],
    options.children,
  );
}

function renderPhoneTopBar(
  h: HtmlBuilder<Message>,
  model: Model,
  activeWorkstream?: Workstream,
): Html {
  const route = model.route;
  const extra: Html[] = [];

  const topBarConfig: { title: string; onBack?: Message } = AppRoute.match(route, {
    CoordinatorChat: () => ({ title: "Chat" }),
    NotFound: () => ({ title: "Chat" }),
    Workstreams: () => ({ title: "Workstreams" }),
    Tasks: () => ({ title: "Tasks" }),
    Inbox: () => ({ title: "Inbox" }),
    Settings: () => ({ title: "Settings" }),
    Roles: () => ({ title: "Roles" }),
    Memory: ({ owner, name }) => ({
      title: pageTitle({ page: "files", owner, name, file: "" }),
    }),
    MemoryFile: ({ owner, name, file }) => ({
      title: pageTitle({ page: "file", owner, name, file }),
    }),
    MemoryRepositories: () => ({ title: "Memory" }),
    SwitchSettings: () => ({ title: "Switches" }),
    Voice: () => ({ title: "Voice" }),
    Devices: () => ({ title: "Devices" }),
    GitHub: () => ({ title: "Connect GitHub" }),
    McpServers: () => ({ title: "MCP servers" }),
    Checkup: () => ({ title: "Checkup" }),
    CheckupTools: () => ({ title: "Tools" }),
    CheckupPermissions: () => ({ title: "App permissions" }),
    CheckupLabels: () => {
      if (Option.isSome(model.checkupProgram)) {
        const fix = fixButtonOf(model.checkupProgram.value);
        if (fix) {
          extra.push(
            h.div(
              [h.Class("mr-2 ml-auto")],
              [
                renderButton(h, {
                  label: fix.text,
                  pending: fix.pending,
                  onClick: Message.GotCheckupMessage({ message: CheckupMsg.ClickedFix() }),
                }),
              ],
            ),
          );
        }
      }
      return { title: "Labels" };
    },
    CheckupWorkstreams: () => ({ title: "Workstreams" }),
    Agents: () => {
      const transcript = Option.flatMap(model.agentsProgram, (agents) => agents.transcript);
      if (Option.isNone(transcript)) {
        return { title: "Agents" };
      }
      return {
        title: agentTitle(transcript.value),
        onBack: Message.GotAgentsMessage({ message: AgentsMessage.BackRequested() }),
      };
    },
    Usage: () => ({ title: "Usage" }),
    WorkstreamChat: ({ owner, name, number }) => {
      const level = shownAutopilot(model, `${owner}/${name}`, number);

      extra.push(
        h.span([h.Class("shrink-0 text-sm text-muted-foreground")], ["#", String(number)]),
      );
      extra.push(
        renderAutopilotSheet(
          h,
          level,
          !activeWorkstream || model.autopilotBusy,
          Option.isSome(model.openMenu) && model.openMenu.value === "autopilot-sheet",
        ),
      );
      extra.push(
        h.button(
          [
            h.Type("button"),
            h.AriaLabel("Brief"),
            h.Title("Brief"),
            h.AriaExpanded(model.mobileBriefOpen),
            h.Class(buttonClass({ variant: "outline", size: "icon-sm", className: "shrink-0" })),
            h.OnClick(Message.SetMobileBriefOpen({ open: true })),
          ],
          [fileTextIcon(h, "size-3.5")],
        ),
      );
      extra.push(
        renderButton(h, {
          label: "Tasks",
          variant: "outline",
          size: "sm",
          className: "shrink-0",
          expanded: model.mobileTasksOpen,
          onClick: Message.SetMobileTasksOpen({ open: true }),
        }),
      );
      return { title: activeWorkstream?.title ?? "" };
    },
  });

  const { title, onBack } = topBarConfig;
  const back = parentPath(route);

  return h.header(
    [h.Class("shrink-0 border-b pt-[env(safe-area-inset-top)] md:hidden")],
    [
      h.div(
        [h.Class("flex min-h-14 items-center gap-2 px-2")],
        [
          back
            ? h.button(
                [
                  h.Type("button"),
                  h.AriaLabel("Back"),
                  h.Class(buttonClass({ variant: "ghost", size: "icon" })),
                  h.OnClick(onBack ?? Message.BackRequested({ parent: back })),
                ],
                [arrowLeftIcon(h)],
              )
            : h.empty,
          h.h1([h.Class(cn("min-w-0 truncate text-base font-semibold", !back && "pl-2"))], [title]),
          ...extra,
        ],
      ),
    ],
  );
}

export function renderFrame(h: HtmlBuilder<Message>, model: Model, content: Html): Html {
  const [currentPath] = routeToPath(model.route).split("?");
  const currentOrg = model.organization;
  const fill = isFillRoute(model.route);
  const showMobileOrgSwitch =
    currentPath === "/chat" ||
    currentPath === "/workstreams" ||
    currentPath === "/tasks" ||
    currentPath.startsWith("/inbox");

  const workstreamLists: WorkstreamLists = {
    list: model.workstreams,
    needsHuman: filterNeedsHuman(model.needsHuman, model.inboxAnswered),
    working: new Set(model.workstreamsWorking),
  };

  // Active workstream if route is WorkstreamChat
  const activeWorkstream = AppRoute.match(model.route, {
    WorkstreamChat: ({ owner, name, number }) =>
      model.workstreams.find((w) => w.repository === `${owner}/${name}` && w.number === number),
    NotFound: () => undefined,
    CoordinatorChat: () => undefined,
    Workstreams: () => undefined,
    Tasks: () => undefined,
    Inbox: () => undefined,
    Settings: () => undefined,
    Roles: () => undefined,
    Memory: () => undefined,
    MemoryFile: () => undefined,
    MemoryRepositories: () => undefined,
    SwitchSettings: () => undefined,
    Voice: () => undefined,
    Devices: () => undefined,
    GitHub: () => undefined,
    McpServers: () => undefined,
    Checkup: () => undefined,
    CheckupTools: () => undefined,
    CheckupPermissions: () => undefined,
    CheckupLabels: () => undefined,
    CheckupWorkstreams: () => undefined,
    Agents: () => undefined,
    Usage: () => undefined,
  });

  // Unread & inbox counts for current org
  const inboxCount = model.inbox.filter((item) => item.organization === currentOrg).length;
  const chatCount = unreadCount(model.unread, {
    organization: currentOrg,
    repository: "",
    workstream: 0,
  });

  const shown = organizationWorkstreams(workstreamLists, currentOrg);
  const evergreenWorkstreams = shown ? shown.filter((w) => w.evergreen) : [];
  const regularWorkstreams = shown ? shown.filter((w) => !w.evergreen) : [];

  const upgradeAvailable = hasUpgradeControls(model);

  // Desktop side nav
  const sideNav = h.nav(
    [
      h.Class(
        cn(
          "sticky top-0 hidden h-svh w-52 shrink-0 flex-col gap-1 overflow-y-auto border-r bg-sidebar p-2 text-sidebar-foreground md:flex",
          Option.isSome(model.openMenu) && model.openMenu.value === "organization-side" && "z-20",
        ),
      ),
    ],
    [
      h.div(
        [h.Class("px-2 py-1")],
        [
          model.organizations.length > 1
            ? renderOrganizationSwitch(h, model, "organization-side")
            : h.span([h.Class("text-base font-semibold")], ["Mobius"]),
        ],
      ),
      renderSideLink(h, {
        path: "/chat",
        currentPath,
        children: [
          h.span([h.Class("grow")], ["Chat"]),
          chatCount > 0 ? renderBadge(h, "default", String(chatCount)) : h.empty,
        ],
      }),
      renderSideLink(h, {
        path: "/inbox",
        currentPath,
        fuzzy: true,
        children: [
          h.span([h.Class("grow")], ["Inbox"]),
          inboxCount > 0 ? renderBadge(h, "default", String(inboxCount)) : h.empty,
        ],
      }),
      renderSideLink(h, {
        path: "/tasks",
        currentPath,
        children: [h.span([h.Class("grow")], ["Tasks"])],
      }),
      ...evergreenWorkstreams.map((workstream) => {
        const { owner, name, number } = chatParams(workstream);
        const linkPath = `/workstreams/${owner}/${name}/${number}`;
        return renderSideLink(h, {
          path: linkPath,
          currentPath,
          className: "h-auto min-h-8 flex-wrap py-1.5 whitespace-normal",
          children: [
            h.span([h.Class("grow")], [workstream.title]),
            renderWorkstreamBadges(h, workstream, workstreamLists, model.unread),
          ],
        });
      }),
      renderSideLink(h, {
        path: "/workstreams",
        currentPath,
        children: [h.span([h.Class("grow")], ["Workstreams"])],
      }),
      ...regularWorkstreams.map((workstream) => {
        const { owner, name, number } = chatParams(workstream);
        const linkPath = `/workstreams/${owner}/${name}/${number}`;
        return renderSideLink(h, {
          path: linkPath,
          currentPath,
          className: "h-auto min-h-8 flex-wrap py-1.5 pl-5 whitespace-normal",
          children: [
            h.span([h.Class("grow")], [workstream.title]),
            renderWorkstreamBadges(h, workstream, workstreamLists, model.unread),
          ],
        });
      }),
      h.div([h.Class("grow")], []),
      renderUpgradeControls(h, model),
      ...settingsPages.map((page) =>
        renderSideLink(h, {
          path: page.path,
          currentPath,
          fuzzy: page.path === "/settings/checkup",
          children: [page.title],
        }),
      ),
    ],
  );

  // Current tab for mobile bottom bar
  const currentTab =
    ["/workstreams", "/inbox"].find((tab) => currentPath.startsWith(tab)) ?? currentPath;

  const mobileBottomNav = h.nav(
    [h.Class("flex shrink-0 border-t bg-sidebar pb-[env(safe-area-inset-bottom)] md:hidden")],
    tabs.map(({ path: tabPath, title, icon: TabIcon }) => {
      const isSelected = currentTab === tabPath;
      let badge: Html = h.empty;
      if (tabPath === "/chat" && chatCount > 0) {
        badge = renderBadge(h, "default", String(chatCount));
      } else if (tabPath === "/inbox" && inboxCount > 0) {
        badge = renderBadge(h, "default", String(inboxCount));
      } else if (tabPath === "/settings" && upgradeAvailable) {
        badge = h.span(
          [
            h.DataAttribute("slot", "badge"),
            h.DataAttribute("variant", "default"),
            h.Class(badgeClass("default")),
            h.AriaLabel("Upgrade available"),
          ],
          [arrowUpIcon(h, "size-3")],
        );
      }

      return h.a(
        [
          h.Key(tabPath),
          h.Attribute("href", tabPath),
          ...(isSelected ? [h.AriaCurrent("page")] : []),
          h.Class(
            cn(
              "flex flex-1 flex-col items-center gap-1 py-2 text-xs",
              isSelected ? "font-medium text-foreground" : "text-muted-foreground",
            ),
          ),
        ],
        [TabIcon(h, "size-5"), h.span([h.Class("flex items-center gap-1")], [title, badge])],
      );
    }),
  );

  const stopsOrganization =
    model.route._tag === "WorkstreamChat"
      ? model.route.owner
      : showMobileOrgSwitch
        ? currentOrg
        : undefined;
  const shownCIStops = model.ciStops.filter(
    (stop) =>
      stopsOrganization === undefined || stop.repository.split("/")[0] === stopsOrganization,
  );

  const connectionStatus = !model.online
    ? "offline"
    : model.link === "connecting"
      ? "connecting"
      : "connected";

  return h.div(
    [h.Class(cn("flex h-svh flex-col md:flex-row", !fill && "md:h-auto md:min-h-svh"))],
    [
      sideNav,
      h.div(
        [h.Class("flex min-h-0 min-w-0 grow flex-col")],
        [
          renderPhoneTopBar(h, model, activeWorkstream),
          h.div(
            [h.Class("sticky top-0 z-10 h-0 shrink-0")],
            [
              renderConnectionBadge(h, connectionStatus),
              model.notice
                ? h.div(
                    [h.Class("pointer-events-none flex justify-center px-4 pt-2")],
                    [renderNotice(h, model.notice)],
                  )
                : h.empty,
            ],
          ),
          h.div(
            [
              h.Id("content"),
              h.DataAttribute("scroll-restoration-id", "content"),
              h.Class(
                cn(
                  "flex min-h-0 grow flex-col overflow-y-auto md:overflow-visible",
                  !fill && "md:p-6",
                ),
              ),
            ],
            [
              h.div(
                [h.Class("grid gap-2 px-4 pt-4 empty:hidden md:hidden")],
                [
                  showMobileOrgSwitch
                    ? renderOrganizationSwitch(h, model, "organization-content")
                    : h.empty,
                ],
              ),
              h.div(
                [
                  h.Class(
                    cn(
                      "grid gap-2 px-4 pt-4 empty:hidden",
                      fill ? "md:px-4" : "md:mx-auto md:w-full md:max-w-3xl md:p-0 md:pb-4",
                    ),
                  ),
                ],
                [
                  ...renderCIStops(h, shownCIStops),
                  ...renderPauses(h, model),
                  renderSwitchLines(h, model),
                ],
              ),
              h.div(
                [h.Class("grid gap-2 px-4 pt-4 empty:hidden md:hidden")],
                [currentPath === "/settings" ? renderUpgradeControls(h, model) : h.empty],
              ),
              h.main(
                [
                  h.Class(
                    cn(
                      fill
                        ? "flex min-h-0 grow"
                        : "grid w-full content-start gap-6 py-4 md:mx-auto md:max-w-3xl md:rounded-xl md:border md:bg-card md:p-6",
                    ),
                  ),
                ],
                [content],
              ),
            ],
          ),
        ],
      ),
      mobileBottomNav,
      renderUpgradeDialog(h, model),
    ],
  );
}

import { createRootRoute, createRoute, createRouter, redirect } from "@tanstack/react-router";
import App from "./App";
import { Devices } from "./Devices";
import {
  ActivityPage,
  AgentsPage,
  ChatPage,
  CheckupLabelsPage,
  CheckupPage,
  CheckupPermissionsPage,
  CheckupToolsPage,
  CheckupWorkstreamsPage,
  GitHubPage,
  InboxPage,
  InboxLayoutPage,
  MemoryFilePage,
  MemoryPage,
  MemoryRepositoriesPage,
  TasksPage,
  CoordinatorChatPage,
  UsagePage,
  WorkstreamsPage,
} from "./pages";
import { validateUsageSearch } from "./lib/usage";
import { Roles } from "./Roles";
import { Settings } from "./Settings";
import { SwitchSettings } from "./SwitchSettings";
import { Voice } from "./Voice";

const rootRoute = createRootRoute({ component: App });

const toChat = () => redirect({ to: "/chat", replace: true });

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  beforeLoad: () => {
    throw toChat();
  },
});

const unknownRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "$",
  beforeLoad: () => {
    throw toChat();
  },
});

const workstreamsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/workstreams",
  component: WorkstreamsPage,
});

const tasksRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/tasks",
  component: TasksPage,
});

const coordinatorChatRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/chat",
  component: CoordinatorChatPage,
});

const chatRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/workstreams/$owner/$name/$number",
  beforeLoad: ({ params }) => {
    if (!/^\d+$/.test(params.number)) {
      throw toChat();
    }
  },
  component: ChatPage,
});

const inboxRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/inbox",
  component: InboxLayoutPage,
});

const inboxTodoRoute = createRoute({
  getParentRoute: () => inboxRoute,
  path: "/",
  component: InboxPage,
});

const inboxActivityRoute = createRoute({
  getParentRoute: () => inboxRoute,
  path: "/activity",
  component: ActivityPage,
});

const agentsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/agents",
  component: AgentsPage,
});

const usageRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/usage",
  validateSearch: validateUsageSearch,
  component: UsagePage,
});

const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/settings",
  component: Settings,
});

const checkupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/settings/checkup",
  component: CheckupPage,
});

const checkupToolsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/settings/checkup/tools",
  component: CheckupToolsPage,
});

const checkupPermissionsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/settings/checkup/$organization/permissions",
  component: CheckupPermissionsPage,
});

const checkupLabelsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/settings/checkup/$organization/labels",
  component: CheckupLabelsPage,
});

const checkupWorkstreamsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/settings/checkup/$organization/workstreams",
  component: CheckupWorkstreamsPage,
});

const memoryRepositoriesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/settings/memory",
  component: MemoryRepositoriesPage,
});

const memoryRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/settings/memory/$owner/$name",
  component: MemoryPage,
});

const memoryFileRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/settings/memory/$owner/$name/$file",
  component: MemoryFilePage,
});

const rolesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/settings/roles",
  component: Roles,
});

const switchSettingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/settings/switches",
  component: SwitchSettings,
});

const voiceRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/settings/voice",
  component: Voice,
});

const devicesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/devices",
  component: Devices,
});

const githubRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/github",
  component: GitHubPage,
});

const routeTree = rootRoute.addChildren([
  indexRoute,
  unknownRoute,
  coordinatorChatRoute,
  workstreamsRoute,
  tasksRoute,
  chatRoute,
  inboxRoute.addChildren([inboxTodoRoute, inboxActivityRoute]),
  agentsRoute,
  usageRoute,
  settingsRoute,
  checkupRoute,
  checkupToolsRoute,
  checkupPermissionsRoute,
  checkupLabelsRoute,
  checkupWorkstreamsRoute,
  memoryRepositoriesRoute,
  memoryRoute,
  memoryFileRoute,
  rolesRoute,
  switchSettingsRoute,
  voiceRoute,
  devicesRoute,
  githubRoute,
]);

export const router = createRouter({
  routeTree,
  scrollRestoration: true,
  scrollToTopSelectors: ["#content"],
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

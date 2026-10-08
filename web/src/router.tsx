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
  GitHubPage,
  InboxPage,
  MemoryPage,
  MemoryRepositoriesPage,
  NewWorkstreamPage,
  WorkstreamsPage,
} from "./pages";
import { Settings } from "./Settings";

const rootRoute = createRootRoute({ component: App });

const toWorkstreams = () => redirect({ to: "/workstreams", replace: true });

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  beforeLoad: () => {
    throw toWorkstreams();
  },
});

const unknownRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "$",
  beforeLoad: () => {
    throw toWorkstreams();
  },
});

const workstreamsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/workstreams",
  component: WorkstreamsPage,
});

const newWorkstreamRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/workstreams/new",
  component: NewWorkstreamPage,
});

const chatRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/workstreams/$owner/$name/$number",
  beforeLoad: ({ params }) => {
    if (!/^\d+$/.test(params.number)) {
      throw toWorkstreams();
    }
  },
  component: ChatPage,
});

const inboxRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/inbox",
  component: InboxPage,
});

const activityRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/activity",
  component: ActivityPage,
});

const agentsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/agents",
  component: AgentsPage,
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
  workstreamsRoute,
  newWorkstreamRoute,
  chatRoute,
  inboxRoute,
  activityRoute,
  agentsRoute,
  settingsRoute,
  checkupRoute,
  checkupToolsRoute,
  checkupPermissionsRoute,
  checkupLabelsRoute,
  memoryRepositoriesRoute,
  memoryRoute,
  devicesRoute,
  githubRoute,
]);

export const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

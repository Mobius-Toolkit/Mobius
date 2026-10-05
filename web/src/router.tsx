import { createRootRoute, createRoute, createRouter, redirect } from "@tanstack/react-router";
import App from "./App";
import { Devices } from "./Devices";
import {
  ActivityPage,
  AgentsPage,
  ChatPage,
  CheckupPage,
  GitHubPage,
  InboxPage,
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
  devicesRoute,
  githubRoute,
]);

export const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

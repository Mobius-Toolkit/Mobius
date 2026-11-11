import { Option, Schema } from "effect";
import { defineRouteUnion } from "foldkit/route";
import type { Url } from "foldkit/url";
import { validateMcpServersSearch } from "@/lib/mcpServers";
import { validateUsageSearch } from "@/lib/usage";
import { Search as UsageSearch } from "@/usage/model";

export const McpSearch = Schema.Struct({
  oauth: Schema.optional(Schema.Literals(["connected", "failed"])),
  server: Schema.optional(Schema.Number),
  reason: Schema.optional(Schema.String),
});

export type McpSearch = typeof McpSearch.Type;

export const AppRoute = defineRouteUnion({
  NotFound: {},
  CoordinatorChat: {},
  Workstreams: {},
  Tasks: {},
  WorkstreamChat: {
    owner: Schema.String,
    name: Schema.String,
    number: Schema.Number,
  },
  Inbox: {},
  Agents: {},
  Usage: {
    search: UsageSearch,
  },
  Settings: {},
  CheckupTools: {},
  CheckupPermissions: {
    organization: Schema.String,
  },
  CheckupLabels: {
    organization: Schema.String,
  },
  CheckupWorkstreams: {
    organization: Schema.String,
  },
  Checkup: {},
  MemoryFile: {
    owner: Schema.String,
    name: Schema.String,
    file: Schema.String,
  },
  Memory: {
    owner: Schema.String,
    name: Schema.String,
  },
  MemoryRepositories: {},
  Roles: {},
  McpServers: {
    search: McpSearch,
  },
  SwitchSettings: {},
  Voice: {},
  Devices: {},
  GitHub: {},
});

export type AppRoute = typeof AppRoute.Type;

const jsonValue = Schema.decodeUnknownOption(Schema.fromJsonString(Schema.Json));
const encodeJson = Schema.encodeSync(Schema.fromJsonString(Schema.Json));

// A value of the search that parses as JSON (a list, a number) is JSON, as in the addresses of earlier releases.
function searchToRecord(search: Option.Option<string>) {
  const result: Record<string, typeof Schema.Json.Type> = {};
  if (Option.isNone(search)) {
    return result;
  }
  for (const [key, value] of new URLSearchParams(search.value)) {
    result[key] = Option.getOrElse(jsonValue(value), () => value);
  }
  return result;
}

function searchQuery(search: Record<string, typeof Schema.Json.Type | undefined>): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(search)) {
    if (value === undefined) {
      continue;
    }
    params.set(
      key,
      typeof value === "string" && Option.isNone(jsonValue(value)) ? value : encodeJson(value),
    );
  }
  const query = params.toString();
  return query ? `?${query}` : "";
}

export function usageSearchToQuery(search: UsageSearch): string {
  return searchQuery(search);
}

export function mcpSearchToQuery(search: McpSearch): string {
  return searchQuery(search);
}

export function parseRoute(url: Url): AppRoute {
  const segments = url.pathname.split("/").filter(Boolean);
  if (segments.length === 0) {
    return AppRoute.NotFound();
  }

  const [first, second, third, fourth, ...rest] = segments;

  switch (first) {
    case "chat":
      return segments.length === 1 ? AppRoute.CoordinatorChat() : AppRoute.NotFound();

    case "workstreams":
      if (segments.length === 1) {
        return AppRoute.Workstreams();
      }
      if (segments.length === 4 && /^\d+$/.test(fourth)) {
        return AppRoute.WorkstreamChat({
          owner: second,
          name: third,
          number: Number(fourth),
        });
      }
      return AppRoute.NotFound();

    case "tasks":
      return segments.length === 1 ? AppRoute.Tasks() : AppRoute.NotFound();

    case "inbox":
      return segments.length === 1 ? AppRoute.Inbox() : AppRoute.NotFound();

    case "agents":
      return segments.length === 1 ? AppRoute.Agents() : AppRoute.NotFound();

    case "usage": {
      if (segments.length !== 1) {
        return AppRoute.NotFound();
      }
      const searchRecord = searchToRecord(url.search);
      const search = validateUsageSearch(searchRecord);
      return AppRoute.Usage({ search });
    }

    case "devices":
      return segments.length === 1 ? AppRoute.Devices() : AppRoute.NotFound();

    case "github":
      return segments.length === 1 ? AppRoute.GitHub() : AppRoute.NotFound();

    case "settings": {
      if (segments.length === 1) {
        return AppRoute.Settings();
      }
      switch (second) {
        case "checkup":
          if (segments.length === 2) {
            return AppRoute.Checkup();
          }
          if (segments.length === 3 && third === "tools") {
            return AppRoute.CheckupTools();
          }
          if (segments.length === 4) {
            if (fourth === "permissions") {
              return AppRoute.CheckupPermissions({ organization: third });
            }
            if (fourth === "labels") {
              return AppRoute.CheckupLabels({ organization: third });
            }
            if (fourth === "workstreams") {
              return AppRoute.CheckupWorkstreams({ organization: third });
            }
          }
          return AppRoute.NotFound();

        case "memory":
          if (segments.length === 2) {
            return AppRoute.MemoryRepositories();
          }
          if (segments.length === 4) {
            return AppRoute.Memory({ owner: third, name: fourth });
          }
          if (segments.length >= 5) {
            const file = rest.join("/");
            return AppRoute.MemoryFile({ owner: third, name: fourth, file });
          }
          return AppRoute.NotFound();

        case "roles":
          return segments.length === 2 ? AppRoute.Roles() : AppRoute.NotFound();

        case "mcp-servers": {
          if (segments.length !== 2) {
            return AppRoute.NotFound();
          }
          const searchRecord = searchToRecord(url.search);
          const search = validateMcpServersSearch(searchRecord);
          return AppRoute.McpServers({ search });
        }

        case "switches":
          return segments.length === 2 ? AppRoute.SwitchSettings() : AppRoute.NotFound();

        case "voice":
          return segments.length === 2 ? AppRoute.Voice() : AppRoute.NotFound();

        default:
          return AppRoute.NotFound();
      }
    }

    default:
      return AppRoute.NotFound();
  }
}

export function routeToPath(route: AppRoute): string {
  return AppRoute.match(route, {
    NotFound: () => "/chat",
    CoordinatorChat: () => "/chat",
    Workstreams: () => "/workstreams",
    Tasks: () => "/tasks",
    WorkstreamChat: ({ owner, name, number }) => `/workstreams/${owner}/${name}/${number}`,
    Inbox: () => "/inbox",
    Agents: () => "/agents",
    Usage: ({ search }) => `/usage${usageSearchToQuery(search)}`,
    Settings: () => "/settings",
    CheckupTools: () => "/settings/checkup/tools",
    CheckupPermissions: ({ organization }) => `/settings/checkup/${organization}/permissions`,
    CheckupLabels: ({ organization }) => `/settings/checkup/${organization}/labels`,
    CheckupWorkstreams: ({ organization }) => `/settings/checkup/${organization}/workstreams`,
    Checkup: () => "/settings/checkup",
    MemoryFile: ({ owner, name, file }) => `/settings/memory/${owner}/${name}/${file}`,
    Memory: ({ owner, name }) => `/settings/memory/${owner}/${name}`,
    MemoryRepositories: () => "/settings/memory",
    Roles: () => "/settings/roles",
    McpServers: ({ search }) => `/settings/mcp-servers${mcpSearchToQuery(search)}`,
    SwitchSettings: () => "/settings/switches",
    Voice: () => "/settings/voice",
    Devices: () => "/devices",
    GitHub: () => "/github",
  });
}

export function isFillRoute(route: AppRoute): boolean {
  return AppRoute.match(route, {
    CoordinatorChat: () => true,
    Tasks: () => true,
    WorkstreamChat: () => true,
    NotFound: () => false,
    Workstreams: () => false,
    Inbox: () => false,
    Agents: () => false,
    Usage: () => false,
    Settings: () => false,
    CheckupTools: () => false,
    CheckupPermissions: () => false,
    CheckupLabels: () => false,
    CheckupWorkstreams: () => false,
    Checkup: () => false,
    MemoryFile: () => false,
    Memory: () => false,
    MemoryRepositories: () => false,
    Roles: () => false,
    McpServers: () => false,
    SwitchSettings: () => false,
    Voice: () => false,
    Devices: () => false,
    GitHub: () => false,
  });
}

export function parentPath(route: AppRoute): string | undefined {
  return AppRoute.match(route, {
    NotFound: () => undefined,
    CoordinatorChat: () => undefined,
    Workstreams: () => undefined,
    Tasks: () => undefined,
    Inbox: () => undefined,
    Settings: () => undefined,
    WorkstreamChat: () => "/workstreams",
    Agents: () => "/settings",
    Usage: () => "/settings",
    CheckupTools: () => "/settings/checkup",
    CheckupPermissions: () => "/settings/checkup",
    CheckupLabels: () => "/settings/checkup",
    CheckupWorkstreams: () => "/settings/checkup",
    Checkup: () => "/settings",
    MemoryFile: ({ owner, name }) => `/settings/memory/${owner}/${name}`,
    Memory: () => "/settings/memory",
    MemoryRepositories: () => "/settings",
    Roles: () => "/settings",
    McpServers: () => "/settings",
    SwitchSettings: () => "/settings",
    Voice: () => "/settings",
    Devices: () => "/settings",
    GitHub: () => "/settings",
  });
}

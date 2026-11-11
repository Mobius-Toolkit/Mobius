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

function searchToRecord(search: Option.Option<string>): Record<string, unknown> {
  if (Option.isNone(search) || !search.value) {
    return {};
  }
  const raw = search.value.startsWith("?") ? search.value.slice(1) : search.value;
  const params = new URLSearchParams(raw);
  const result: Record<string, unknown> = {};
  for (const [key, value] of params.entries()) {
    const existing = result[key];
    if (existing !== undefined) {
      if (Array.isArray(existing)) {
        existing.push(value);
      } else {
        result[key] = [existing, value];
      }
    } else {
      result[key] = value;
    }
  }
  return result;
}

export function usageSearchToQuery(search: UsageSearch): string {
  const params = new URLSearchParams();
  if (search.tab) params.set("tab", search.tab);
  if (search.period) params.set("period", search.period);
  if (search.from) params.set("from", search.from);
  if (search.to) params.set("to", search.to);
  if (search.stat) params.set("stat", search.stat);
  if (search.value) params.set("value", search.value);
  if (search.group) {
    for (const g of search.group) params.append("group", g);
  }
  const arrayKeys = [
    "harness",
    "model",
    "modelVersion",
    "effort",
    "role",
    "repository",
    "source",
  ] as const;
  for (const key of arrayKeys) {
    const val = search[key];
    if (val) {
      for (const item of val) params.append(key, item);
    }
  }
  const str = params.toString();
  return str ? `?${str}` : "";
}

export function mcpSearchToQuery(search: McpSearch): string {
  const params = new URLSearchParams();
  if (search.oauth) params.set("oauth", search.oauth);
  if (search.server !== undefined) params.set("server", String(search.server));
  if (search.reason) params.set("reason", search.reason);
  const str = params.toString();
  return str ? `?${str}` : "";
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
            const file = [fourth, ...rest].join("/");
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

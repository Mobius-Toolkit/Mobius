import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import * as Port from "foldkit/port";
import { getUsage, getUsageQuality, getUsageSkills, getUsageTimes } from "@/api/api.gen";
import { request as send } from "@/lib/effects";
import { groupOptions, qualityGroups, skillGroups } from "@/lib/usage";
import { Message } from "./message";
import { groupFieldsOf, Range, Search } from "./model";
import { ports } from "./ports";

const copy = (values: readonly string[] | undefined) =>
  values === undefined ? undefined : [...values];

const isoOf = (time: number) => new Date(time).toISOString();

function usageParams(search: Search, range: Range, timeZone: string) {
  const fields = groupFieldsOf(search, groupOptions);
  return {
    from: isoOf(range.from),
    to: isoOf(range.to),
    tz: timeZone,
    harness: copy(search.harness),
    model: copy(search.model),
    modelVersion: copy(search.modelVersion),
    effort: copy(search.effort),
    role: copy(search.role),
    repository: copy(search.repository),
    group: fields.length > 0 ? fields : undefined,
  };
}

const args = {
  request: Schema.Number,
  search: Search,
  range: Range,
  timeZone: Schema.String,
};

export const FetchUsage = Command.define("FetchUsage", {
  args,
  messages: [Message.LoadedUsage, Message.LoadLoginRequired, Message.LoadFailed],
  execute: ({ request, search, range, timeZone }) =>
    send(() => getUsage(usageParams(search, range, timeZone))).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoadLoginRequired({ request });
        }
        if (res.status !== 200) {
          return Message.LoadFailed({ request, error: res.data.error });
        }
        return Message.LoadedUsage({ request, usage: res.data.data, range });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.LoadFailed({ request, error: error.message })),
      ),
    ),
});

export const FetchTimes = Command.define("FetchTimes", {
  args,
  messages: [Message.LoadedTimes, Message.LoadLoginRequired, Message.LoadFailed],
  execute: ({ request, search, range, timeZone }) =>
    send(() => getUsageTimes(usageParams(search, range, timeZone))).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoadLoginRequired({ request });
        }
        if (res.status !== 200) {
          return Message.LoadFailed({ request, error: res.data.error });
        }
        return Message.LoadedTimes({ request, times: res.data.data, range });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.LoadFailed({ request, error: error.message })),
      ),
    ),
});

export const FetchSkills = Command.define("FetchSkills", {
  args,
  messages: [Message.LoadedSkills, Message.LoadLoginRequired, Message.LoadFailed],
  execute: ({ request, search, range }) => {
    const fields = groupFieldsOf(search, skillGroups);
    return send(() =>
      getUsageSkills({
        from: isoOf(range.from),
        to: isoOf(range.to),
        harness: copy(search.harness),
        role: copy(search.role),
        repository: copy(search.repository),
        source: copy(search.source),
        group: fields.length > 0 ? fields : undefined,
      }),
    ).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoadLoginRequired({ request });
        }
        if (res.status !== 200) {
          return Message.LoadFailed({ request, error: res.data.error });
        }
        return Message.LoadedSkills({ request, skills: res.data.data });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.LoadFailed({ request, error: error.message })),
      ),
    );
  },
});

export const FetchQuality = Command.define("FetchQuality", {
  args,
  messages: [Message.LoadedQuality, Message.LoadLoginRequired, Message.LoadFailed],
  execute: ({ request, search, range, timeZone }) => {
    const fields = groupFieldsOf(search, qualityGroups);
    return send(() =>
      getUsageQuality({
        from: isoOf(range.from),
        to: isoOf(range.to),
        tz: timeZone,
        harness: copy(search.harness),
        model: copy(search.model),
        effort: copy(search.effort),
        repository: copy(search.repository),
        group: fields.length > 0 ? fields : undefined,
      }),
    ).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoadLoginRequired({ request });
        }
        if (res.status !== 200) {
          return Message.LoadFailed({ request, error: res.data.error });
        }
        return Message.LoadedQuality({ request, quality: res.data.data, range });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.LoadFailed({ request, error: error.message })),
      ),
    );
  },
});

export const ChangeSearch = Command.define("ChangeSearch", {
  args: { search: Search },
  messages: [Message.CompletedChangeSearch],
  execute: ({ search }) =>
    Port.emit(ports.outbound.searchChanged, search).pipe(
      Effect.as(Message.CompletedChangeSearch()),
    ),
});

export const RequestLogin = Command.define("RequestLogin", {
  messages: [Message.CompletedRequestLogin],
  execute: Port.emit(ports.outbound.loginRequested, undefined).pipe(
    Effect.as(Message.CompletedRequestLogin()),
  ),
});

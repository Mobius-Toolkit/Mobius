import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import * as Port from "foldkit/port";
import { listMemoryFiles, listMemoryVersions, revertMemory, saveMemory } from "@/api/api.gen";
import { answer, request } from "@/lib/effects";
import { Message } from "./message";
import { ports } from "./ports";

const place = { owner: Schema.String, name: Schema.String, file: Schema.String };

export const FetchFiles = Command.define("FetchFiles", {
  args: { owner: Schema.String, name: Schema.String },
  messages: [Message.FilesLoaded, Message.LoginRequired, Message.FilesFailed],
  execute: ({ owner, name }) =>
    request(() => listMemoryFiles(owner, name)).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status !== 200) {
          return Message.FilesFailed({ error: res.data.error });
        }
        return Message.FilesLoaded({ files: res.data.data });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.FilesFailed({ error: error.message })),
      ),
    ),
});

export const FetchVersions = Command.define("FetchVersions", {
  args: place,
  messages: [Message.VersionsLoaded, Message.LoginRequired, Message.VersionsFailed],
  execute: ({ owner, name, file }) =>
    request(() => listMemoryVersions(owner, name, file)).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status !== 200) {
          return Message.VersionsFailed({ error: res.data.error });
        }
        return Message.VersionsLoaded({
          versions: res.data.data.versions,
          older: res.data.data.older,
        });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.VersionsFailed({ error: error.message })),
      ),
    ),
});

export const FetchOlder = Command.define("FetchOlder", {
  args: { ...place, before: Schema.Number },
  messages: [Message.OlderLoaded, Message.LoginRequired, Message.OlderFailed],
  execute: ({ owner, name, file, before }) =>
    request(() => listMemoryVersions(owner, name, file, { before })).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status !== 200) {
          return Message.OlderFailed({ error: res.data.error });
        }
        return Message.OlderLoaded({
          versions: res.data.data.versions,
          older: res.data.data.older,
        });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.OlderFailed({ error: error.message })),
      ),
    ),
});

export const SaveMemory = Command.define("SaveMemory", {
  args: { ...place, text: Schema.String, base: Schema.Number },
  messages: [Message.Saved, Message.SaveLoginRequired, Message.SaveFailed],
  execute: ({ owner, name, file, text, base }) =>
    answer(() => saveMemory(owner, name, file, { text, base_version: base }), {
      done: Message.Saved,
      login: Message.SaveLoginRequired,
      failed: (error) => Message.SaveFailed({ error }),
    }),
});

export const RevertMemory = Command.define("RevertMemory", {
  args: { ...place, id: Schema.Number },
  messages: [Message.Reverted, Message.RevertLoginRequired, Message.RevertFailed],
  execute: ({ owner, name, file, id }) =>
    answer(() => revertMemory(owner, name, file, id), {
      done: Message.Reverted,
      login: Message.RevertLoginRequired,
      failed: (error) => Message.RevertFailed({ error }),
    }),
});

export const RequestLogin = Command.define("RequestLogin", {
  messages: [Message.CompletedRequestLogin],
  execute: Port.emit(ports.outbound.loginRequested, undefined).pipe(
    Effect.as(Message.CompletedRequestLogin()),
  ),
});

export const Navigate = Command.define("Navigate", {
  args: { path: Schema.String },
  messages: [Message.CompletedNavigate],
  execute: ({ path }) =>
    Port.emit(ports.outbound.navigationRequested, path).pipe(
      Effect.as(Message.CompletedNavigate()),
    ),
});

export const GoBack = Command.define("GoBack", {
  messages: [Message.CompletedGoBack],
  execute: Port.emit(ports.outbound.backRequested, undefined).pipe(
    Effect.as(Message.CompletedGoBack()),
  ),
});

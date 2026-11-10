import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import * as Port from "foldkit/port";
import {
  addMcpServer,
  changeMcpServer,
  deleteMcpServer,
  disconnectMcpOAuth,
  listMcpServers,
  startMcpOAuth,
} from "@/api/api.gen";
import { answer, request } from "@/lib/effects";
import { showNotice } from "@/lib/notice";
import { Message } from "./message";
import { ports } from "./ports";
import { Body } from "./schemas";

const bodyOf = (body: Body) => ({
  ...body,
  args: [...body.args],
  headers: [...body.headers],
  env: [...body.env],
});

export const FetchServers = Command.define("FetchServers", {
  args: { organization: Schema.String },
  messages: [Message.ServersLoaded, Message.LoginRequired, Message.ServersFailed],
  execute: ({ organization }) =>
    request(() => listMcpServers(organization)).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status !== 200) {
          return Message.ServersFailed({ error: res.data.error });
        }
        return Message.ServersLoaded({ servers: res.data.data });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.ServersFailed({ error: error.message })),
      ),
    ),
});

export const AddServer = Command.define("AddServer", {
  args: { organization: Schema.String, body: Body },
  messages: [Message.ServerSaved, Message.SaveLoginRequired, Message.SaveFailed],
  execute: ({ organization, body }) =>
    request(() => addMcpServer(organization, bodyOf(body))).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.SaveLoginRequired();
        }
        if (res.status !== 201) {
          return Message.SaveFailed({ error: res.data.error });
        }
        showNotice(`Added ${res.data.data.name}`);
        return Message.ServerSaved({ server: res.data.data });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.SaveFailed({ error: error.message })),
      ),
    ),
});

export const ChangeServer = Command.define("ChangeServer", {
  args: { organization: Schema.String, id: Schema.Number, body: Body },
  messages: [Message.ServerSaved, Message.SaveLoginRequired, Message.SaveFailed],
  execute: ({ organization, id, body }) =>
    request(() => changeMcpServer(organization, id, bodyOf(body))).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.SaveLoginRequired();
        }
        if (res.status !== 200) {
          return Message.SaveFailed({ error: res.data.error });
        }
        showNotice(`Changed ${res.data.data.name}`);
        return Message.ServerSaved({ server: res.data.data });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.SaveFailed({ error: error.message })),
      ),
    ),
});

export const DeleteServer = Command.define("DeleteServer", {
  args: { organization: Schema.String, id: Schema.Number, name: Schema.String },
  messages: [Message.ServerDeleted, Message.LoginRequired, Message.DeleteFailed],
  execute: ({ organization, id, name }) =>
    answer(() => deleteMcpServer(organization, id), {
      done: () => {
        showNotice(`Deleted ${name}`);
        return Message.ServerDeleted({ id });
      },
      login: Message.LoginRequired,
      failed: (error) => Message.DeleteFailed({ error }),
    }),
});

export const StartOAuth = Command.define("StartOAuth", {
  args: {
    organization: Schema.String,
    id: Schema.Number,
    clientId: Schema.String,
    clientSecret: Schema.String,
  },
  messages: [Message.OAuthStarted, Message.LoginRequired, Message.OAuthFailed],
  execute: ({ organization, id, clientId, clientSecret }) =>
    request(() => startMcpOAuth(organization, id, { clientId, clientSecret })).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status !== 200) {
          return Message.OAuthFailed({ error: res.data.error });
        }
        return Message.OAuthStarted({ url: res.data.data.url });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.OAuthFailed({ error: error.message })),
      ),
    ),
});

export const DisconnectOAuth = Command.define("DisconnectOAuth", {
  args: { organization: Schema.String, id: Schema.Number, name: Schema.String },
  messages: [Message.OAuthDisconnected, Message.LoginRequired, Message.OAuthFailed],
  execute: ({ organization, id, name }) =>
    answer(() => disconnectMcpOAuth(organization, id), {
      done: () => {
        showNotice(`Disconnected ${name}`);
        return Message.OAuthDisconnected({ id });
      },
      login: Message.LoginRequired,
      failed: (error) => Message.OAuthFailed({ error }),
    }),
});

export const Redirect = Command.define("Redirect", {
  args: { url: Schema.String },
  messages: [Message.CompletedRedirect],
  execute: ({ url }) =>
    Effect.sync(() => {
      window.location.assign(url);
      return Message.CompletedRedirect();
    }),
});

export const ShowNotice = Command.define("ShowNotice", {
  args: { text: Schema.String },
  messages: [Message.CompletedShowNotice],
  execute: ({ text }) =>
    Effect.sync(() => {
      showNotice(text);
      return Message.CompletedShowNotice();
    }),
});

export const Navigate = Command.define("Navigate", {
  args: { path: Schema.String },
  messages: [Message.CompletedNavigate],
  execute: ({ path }) =>
    Port.emit(ports.outbound.navigationRequested, path).pipe(
      Effect.as(Message.CompletedNavigate()),
    ),
});

export const RequestLogin = Command.define("RequestLogin", {
  messages: [Message.CompletedRequestLogin],
  execute: Port.emit(ports.outbound.loginRequested, undefined).pipe(
    Effect.as(Message.CompletedRequestLogin()),
  ),
});

export const GoBack = Command.define("GoBack", {
  messages: [Message.CompletedGoBack],
  execute: Port.emit(ports.outbound.backRequested, undefined).pipe(
    Effect.as(Message.CompletedGoBack()),
  ),
});

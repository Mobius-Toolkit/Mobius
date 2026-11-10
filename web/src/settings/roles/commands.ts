import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import * as Port from "foldkit/port";
import { listHarnesses, listRoles, resetRole, setRole } from "@/api/api.gen";
import { request } from "@/lib/effects";
import { RoleName } from "../schemas";
import { Message } from "./message";
import { ports } from "./ports";

const isRoleName = Schema.is(RoleName);

export const FetchRoles = Command.define("FetchRoles", {
  messages: [Message.RolesLoaded, Message.LoginRequired, Message.RolesFailed],
  execute: request(() => listRoles()).pipe(
    Effect.map((res) => {
      if (res.status === 401) {
        return Message.LoginRequired();
      }
      if (res.status !== 200) {
        return Message.RolesFailed({ error: res.data.error });
      }
      return Message.RolesLoaded({
        roles: res.data.data.flatMap((role) =>
          isRoleName(role.role) ? [{ ...role, role: role.role }] : [],
        ),
      });
    }),
    Effect.catchTag("RequestFailed", (error) =>
      Effect.succeed(Message.RolesFailed({ error: error.message })),
    ),
  ),
});

export const FetchHarnesses = Command.define("FetchHarnesses", {
  messages: [Message.HarnessesLoaded, Message.LoginRequired, Message.HarnessesFailed],
  execute: request(() => listHarnesses()).pipe(
    Effect.map((res) => {
      if (res.status === 401) {
        return Message.LoginRequired();
      }
      if (res.status !== 200) {
        return Message.HarnessesFailed({ error: res.data.error });
      }
      return Message.HarnessesLoaded({ harnesses: res.data.data });
    }),
    Effect.catchTag("RequestFailed", (error) =>
      Effect.succeed(Message.HarnessesFailed({ error: error.message })),
    ),
  ),
});

export const SaveRole = Command.define("SaveRole", {
  args: {
    role: RoleName,
    harness: Schema.String,
    model: Schema.String,
    effort: Schema.String,
    context: Schema.Number,
    max: Schema.Number,
  },
  messages: [Message.RoleChanged, Message.SaveLoginRequired, Message.SaveFailed],
  execute: ({ role, harness, model, effort, context, max }) =>
    request(() => setRole(role, { harness, model, effort, context, max })).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.SaveLoginRequired();
        }
        if (res.status !== 200) {
          return Message.SaveFailed({ error: res.data.error });
        }
        return Message.RoleChanged({ role: { ...res.data.data, role } });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.SaveFailed({ error: error.message })),
      ),
    ),
});

export const ResetRole = Command.define("ResetRole", {
  args: { role: RoleName },
  messages: [Message.RoleChanged, Message.SaveLoginRequired, Message.SaveFailed],
  execute: ({ role }) =>
    request(() => resetRole(role)).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.SaveLoginRequired();
        }
        if (res.status !== 200) {
          return Message.SaveFailed({ error: res.data.error });
        }
        return Message.RoleChanged({ role: { ...res.data.data, role } });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.SaveFailed({ error: error.message })),
      ),
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

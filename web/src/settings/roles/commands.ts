import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import {
  addImplementerBinding,
  listHarnesses,
  listRoles,
  removeImplementerBinding,
  resetRole,
  setImplementerBinding,
  setRole,
} from "@/api/api.gen";
import { request } from "@/lib/effects";
import { RoleName } from "../schemas";
import { Message } from "./message";

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

const bindingArgs = {
  harness: Schema.String,
  model: Schema.String,
  effort: Schema.String,
  context: Schema.Number,
  max: Schema.Number,
};

export const AddBinding = Command.define("AddBinding", {
  args: bindingArgs,
  messages: [Message.BindingsChanged, Message.SaveLoginRequired, Message.SaveFailed],
  execute: (body) =>
    request(() => addImplementerBinding(body)).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.SaveLoginRequired();
        }
        if (res.status !== 200) {
          return Message.SaveFailed({ error: res.data.error });
        }
        const added = res.data.data.moreBindings.at(-1);
        return Message.BindingsChanged({
          role: { ...res.data.data, role: "implementer" },
          tab: added === undefined ? "0" : String(added.id),
        });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.SaveFailed({ error: error.message })),
      ),
    ),
});

export const SetBinding = Command.define("SetBinding", {
  args: { id: Schema.Number, ...bindingArgs },
  messages: [Message.BindingsChanged, Message.SaveLoginRequired, Message.SaveFailed],
  execute: ({ id, ...body }) =>
    request(() => setImplementerBinding(id, body)).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.SaveLoginRequired();
        }
        if (res.status !== 200) {
          return Message.SaveFailed({ error: res.data.error });
        }
        return Message.BindingsChanged({
          role: { ...res.data.data, role: "implementer" },
          tab: String(id),
        });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.SaveFailed({ error: error.message })),
      ),
    ),
});

export const RemoveBinding = Command.define("RemoveBinding", {
  args: { id: Schema.Number },
  messages: [Message.BindingsChanged, Message.SaveLoginRequired, Message.SaveFailed],
  execute: ({ id }) =>
    request(() => removeImplementerBinding(id)).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.SaveLoginRequired();
        }
        if (res.status !== 200) {
          return Message.SaveFailed({ error: res.data.error });
        }
        return Message.BindingsChanged({
          role: { ...res.data.data, role: "implementer" },
          tab: "0",
        });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.SaveFailed({ error: error.message })),
      ),
    ),
});

export const RequestLogin = Command.define("RequestLogin", {
  messages: [Message.CompletedRequestLogin],
  execute: Effect.sync(() => {
    window.dispatchEvent(new CustomEvent("mobius:login"));
  }).pipe(Effect.as(Message.CompletedRequestLogin())),
});

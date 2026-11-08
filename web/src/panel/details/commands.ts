import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import {
  closeWorkstream,
  getWorkstreamDetails,
  listRoles,
  setEvergreen,
  workstreamClosure,
} from "@/api/api.gen";
import { answer, request } from "@/lib/effects";
import { showNotice } from "@/lib/notice";
import { Message } from "./message";

const Workstream = { owner: Schema.String, name: Schema.String, number: Schema.Number };

export const FetchDetails = Command.define("FetchDetails", {
  args: Workstream,
  messages: [Message.DetailsLoaded, Message.LoginRequired, Message.LoadFailed],
  execute: ({ owner, name, number }) =>
    request(() => getWorkstreamDetails(owner, name, number)).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status !== 200) {
          return Message.LoadFailed({ error: res.data.error });
        }
        return Message.DetailsLoaded({ details: res.data.data });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.LoadFailed({ error: error.message })),
      ),
    ),
});

export const FetchRoles = Command.define("FetchRoles", {
  messages: [Message.RolesLoaded, Message.LoadFailed],
  execute: request(() => listRoles()).pipe(
    Effect.map((res) =>
      res.status === 200
        ? Message.RolesLoaded({ roles: res.data.data })
        : Message.LoadFailed({ error: res.data.error }),
    ),
    Effect.catchTag("RequestFailed", (error) =>
      Effect.succeed(Message.LoadFailed({ error: error.message })),
    ),
  ),
});

export const SetEvergreen = Command.define("SetEvergreen", {
  args: { ...Workstream, on: Schema.Boolean },
  messages: [Message.EvergreenChanged, Message.LoginRequired, Message.EvergreenFailed],
  execute: ({ owner, name, number, on }) =>
    answer(() => setEvergreen(owner, name, number, { on }), {
      done: () => {
        showNotice(`Evergreen is ${on ? "on" : "off"} for #${number}`);
        return Message.EvergreenChanged({ on });
      },
      login: Message.LoginRequired,
      failed: (error) => Message.EvergreenFailed({ error }),
    }),
});

export const FetchClosure = Command.define("FetchClosure", {
  args: Workstream,
  messages: [Message.ClosureLoaded, Message.LoginRequired, Message.ClosureFailed],
  execute: ({ owner, name, number }) =>
    request(() => workstreamClosure(owner, name, number)).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status !== 200) {
          return Message.ClosureFailed({ error: res.data.error });
        }
        return Message.ClosureLoaded({ items: res.data.data });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.ClosureFailed({ error: error.message })),
      ),
    ),
});

export const CloseWorkstream = Command.define("CloseWorkstream", {
  args: Workstream,
  messages: [Message.WorkstreamClosed, Message.CloseFailed],
  execute: ({ owner, name, number }) =>
    answer(() => closeWorkstream(owner, name, number), {
      done: () => {
        showNotice(`Closed the Workstream #${number} as won't do`);
        return Message.WorkstreamClosed();
      },
      failed: (error) => Message.CloseFailed({ error }),
    }),
});

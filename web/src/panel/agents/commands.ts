import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import { listAgents } from "@/api/api.gen";
import { request } from "@/lib/effects";
import { Message } from "./message";

export const FetchAgents = Command.define("FetchAgents", {
  args: { owner: Schema.String, name: Schema.String, number: Schema.Number },
  messages: [Message.AgentsLoaded, Message.LoginRequired, Message.LoadFailed],
  execute: ({ owner, name, number }) =>
    request(() => listAgents(owner, name, number)).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status !== 200) {
          return Message.LoadFailed({ error: res.data.error });
        }
        return Message.AgentsLoaded({ agents: res.data.data });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.LoadFailed({ error: error.message })),
      ),
    ),
});

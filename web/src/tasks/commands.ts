import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import * as Port from "foldkit/port";
import { listBoardTasks } from "@/api/api.gen";
import { request } from "@/lib/effects";
import { Message } from "./message";
import { ports } from "./ports";

export const FetchBoard = Command.define("FetchBoard", {
  messages: [Message.BoardLoaded, Message.LoginRequired, Message.BoardFailed],
  execute: request(() => listBoardTasks()).pipe(
    Effect.map((res) => {
      if (res.status === 401) {
        return Message.LoginRequired();
      }
      if (res.status !== 200) {
        return Message.BoardFailed({ error: res.data.error });
      }
      return Message.BoardLoaded({ tasks: res.data.data });
    }),
    Effect.catchTag("RequestFailed", (error) =>
      Effect.succeed(Message.BoardFailed({ error: error.message })),
    ),
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

export const RequestLogin = Command.define("RequestLogin", {
  messages: [Message.CompletedRequestLogin],
  execute: Port.emit(ports.outbound.loginRequested, undefined).pipe(
    Effect.as(Message.CompletedRequestLogin()),
  ),
});

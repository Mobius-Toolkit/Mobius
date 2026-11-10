import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import * as Port from "foldkit/port";
import { listDevices, logout } from "@/api/api.gen";
import { answer, request } from "@/lib/effects";
import { Message } from "./message";
import { ports } from "./ports";

export const FetchDevices = Command.define("FetchDevices", {
  messages: [Message.DevicesLoaded, Message.LoginRequired, Message.LoadFailed],
  execute: request(() => listDevices()).pipe(
    Effect.map((res) => {
      if (res.status === 401) {
        return Message.LoginRequired();
      }
      if (res.status !== 200) {
        return Message.LoadFailed({ error: res.data.error });
      }
      return Message.DevicesLoaded({ devices: res.data.data });
    }),
    Effect.catchTag("RequestFailed", (error) =>
      Effect.succeed(Message.LoadFailed({ error: error.message })),
    ),
  ),
});

export const LogOut = Command.define("LogOut", {
  args: { id: Schema.Number },
  messages: [Message.LoggedOut, Message.LogOutLoginRequired, Message.LogOutFailed],
  execute: ({ id }) =>
    answer(() => logout(id), {
      done: () => Message.LoggedOut({ id }),
      login: () => Message.LogOutLoginRequired({ id }),
      failed: (error) => Message.LogOutFailed({ id, error }),
    }),
});

export const RequestLogin = Command.define("RequestLogin", {
  messages: [Message.CompletedRequestLogin],
  execute: Port.emit(ports.outbound.loginRequested, undefined).pipe(
    Effect.as(Message.CompletedRequestLogin()),
  ),
});

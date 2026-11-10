import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import * as Port from "foldkit/port";
import { getVoiceLanguage, setVoiceLanguage } from "@/api/api.gen";
import { request } from "@/lib/effects";
import { Message } from "./message";
import { ports } from "./ports";

export const FetchVoice = Command.define("FetchVoice", {
  messages: [Message.VoiceLoaded, Message.LoginRequired, Message.LoadFailed],
  execute: request(() => getVoiceLanguage()).pipe(
    Effect.map((res) => {
      if (res.status === 401) {
        return Message.LoginRequired();
      }
      if (res.status !== 200) {
        return Message.LoadFailed({ error: res.data.error });
      }
      return Message.VoiceLoaded({ voice: res.data.data });
    }),
    Effect.catchTag("RequestFailed", (error) =>
      Effect.succeed(Message.LoadFailed({ error: error.message })),
    ),
  ),
});

export const SaveLanguage = Command.define("SaveLanguage", {
  args: { code: Schema.String },
  messages: [Message.LanguageSaved, Message.LoginRequired, Message.SaveFailed],
  execute: ({ code }) =>
    request(() => setVoiceLanguage({ language: code })).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status !== 200) {
          return Message.SaveFailed({ error: res.data.error });
        }
        return Message.LanguageSaved({ voice: res.data.data });
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

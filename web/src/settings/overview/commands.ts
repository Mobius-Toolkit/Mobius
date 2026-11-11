import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import { pushUrl } from "@/lib/navigation";
import { getVersion, getVoiceLanguage } from "@/api/api.gen";
import { request } from "@/lib/effects";
import { Message } from "./message";

export const FetchVersion = Command.define("FetchVersion", {
  messages: [Message.VersionLoaded, Message.LoginRequired, Message.ReadFailed],
  execute: request(() => getVersion()).pipe(
    Effect.map((res) => {
      if (res.status === 401) {
        return Message.LoginRequired();
      }
      if (res.status !== 200) {
        return Message.ReadFailed();
      }
      return Message.VersionLoaded({ release: res.data.data.release });
    }),
    Effect.catchTag("RequestFailed", () => Effect.succeed(Message.ReadFailed())),
  ),
});

export const FetchVoice = Command.define("FetchVoice", {
  messages: [Message.VoiceLoaded, Message.LoginRequired, Message.ReadFailed],
  execute: request(() => getVoiceLanguage()).pipe(
    Effect.map((res) => {
      if (res.status === 401) {
        return Message.LoginRequired();
      }
      if (res.status !== 200) {
        return Message.ReadFailed();
      }
      return Message.VoiceLoaded({ voice: res.data.data });
    }),
    Effect.catchTag("RequestFailed", () => Effect.succeed(Message.ReadFailed())),
  ),
});

export const RequestLogin = Command.define("RequestLogin", {
  messages: [Message.CompletedRequestLogin],
  execute: Effect.sync(() => {
    window.dispatchEvent(new CustomEvent("mobius:login"));
  }).pipe(Effect.as(Message.CompletedRequestLogin())),
});

export const Navigate = Command.define("Navigate", {
  args: { path: Schema.String },
  messages: [Message.CompletedNavigate],
  execute: ({ path }) => pushUrl(path).pipe(Effect.as(Message.CompletedNavigate())),
});

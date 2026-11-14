import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import { ChatSelection } from "@/chat/schemas";
import { addDraftSelection } from "@/lib/draft";
import { store } from "@/lib/effects";
import { pushUrl } from "@/lib/navigation";
import { newMessageId } from "@/lib/outbox";
import { Message } from "./message";
import { Tab } from "./schemas";

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

export const ReportTab = Command.define("ReportTab", {
  args: { tab: Tab },
  messages: [Message.CompletedReportTab],
  execute: () => Effect.succeed(Message.CompletedReportTab()),
});

export const AddSelection = Command.define("AddSelection", {
  args: { key: Schema.String, selection: ChatSelection },
  messages: [Message.AddedSelection],
  execute: ({ key, selection }) =>
    // A draft that the store refused leaves the draft as it was.
    store(() => addDraftSelection(key, { ...selection, id: newMessageId() })).pipe(
      Effect.ignore,
      Effect.as(Message.AddedSelection()),
    ),
});

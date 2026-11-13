import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import { getCheckup, setIssueRepository } from "@/api/api.gen";
import { request, store } from "@/lib/effects";
import { pushUrl } from "@/lib/navigation";
import { showNotice } from "@/lib/notice";
import { addCoordinatorMessage } from "@/lib/outbox";
import { Message } from "./message";

export const FetchCheckup = Command.define("FetchCheckup", {
  args: { organization: Schema.String },
  messages: [Message.CheckupLoaded, Message.LoginRequired, Message.CheckupFailed],
  execute: ({ organization }) =>
    request(() => getCheckup({ organization })).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status !== 200) {
          return Message.CheckupFailed({ error: res.data.error });
        }
        return Message.CheckupLoaded({ checkup: res.data.data });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.CheckupFailed({ error: error.message })),
      ),
    ),
});

export const SaveIssueRepository = Command.define("SaveIssueRepository", {
  args: { organization: Schema.String, repository: Schema.String },
  messages: [Message.Saved, Message.LoginRequired, Message.SaveFailed],
  execute: ({ organization, repository }) =>
    request(() => setIssueRepository({ organization, repository })).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status !== 200) {
          return Message.SaveFailed({ error: res.data.error });
        }
        showNotice(`Set the issue repository of ${organization} to ${repository}`);
        return Message.Saved();
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.SaveFailed({ error: error.message })),
      ),
    ),
});

export const CreateWorkstream = Command.define("CreateWorkstream", {
  args: { organization: Schema.String, request: Schema.String },
  messages: [Message.WorkstreamRequested, Message.CreateFailed],
  execute: ({ organization, request: text }) =>
    store(() => addCoordinatorMessage(organization, text)).pipe(
      Effect.as(Message.WorkstreamRequested({ organization })),
      Effect.catchTag("StoreFailed", (error) =>
        Effect.succeed(Message.CreateFailed({ error: error.message })),
      ),
    ),
});

export const Navigate = Command.define("Navigate", {
  args: { path: Schema.String },
  messages: [Message.CompletedNavigate],
  execute: ({ path }) => pushUrl(path).pipe(Effect.as(Message.CompletedNavigate())),
});

export const RequestLogin = Command.define("RequestLogin", {
  messages: [Message.CompletedRequestLogin],
  execute: Effect.sync(() => {
    window.dispatchEvent(new CustomEvent("mobius:login"));
  }).pipe(Effect.as(Message.CompletedRequestLogin())),
});

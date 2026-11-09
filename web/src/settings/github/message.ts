import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import { GitHubApp } from "../schemas";

export const Message = defineMessageUnion({
  ReceivedApps: { apps: Schema.Array(GitHubApp) },
  ChangedAccount: { text: Schema.String },
  ChangedName: { text: Schema.String },
  SubmittedForm: {},
  FormPosted: {},
  CreateFailed: { error: Schema.String },
  LoginRequired: {},
  CompletedRequestLogin: {},
});

export type Message = typeof Message.Type;

import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import { createManifestForm, type ManifestForm } from "@/api/api.gen";
import { request } from "@/lib/effects";
import { Message } from "./message";

function postToGitHub(form: ManifestForm) {
  const element = document.createElement("form");
  element.method = "post";
  element.action = form.url;
  const field = document.createElement("input");
  field.type = "hidden";
  field.name = "manifest";
  field.value = form.manifest;
  element.append(field);
  document.body.append(element);
  element.submit();
}

export const CreateApp = Command.define("CreateApp", {
  args: { account: Schema.String, name: Schema.String },
  messages: [Message.FormPosted, Message.LoginRequired, Message.CreateFailed],
  execute: ({ account, name }) =>
    request(() => createManifestForm({ account, name, origin: window.location.origin })).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status !== 200) {
          return Message.CreateFailed({ error: res.data.error });
        }
        postToGitHub(res.data.data);
        return Message.FormPosted();
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.CreateFailed({ error: error.message })),
      ),
    ),
});

export const RequestLogin = Command.define("RequestLogin", {
  messages: [Message.CompletedRequestLogin],
  execute: Effect.sync(() => {
    window.dispatchEvent(new CustomEvent("mobius:login"));
  }).pipe(Effect.as(Message.CompletedRequestLogin())),
});

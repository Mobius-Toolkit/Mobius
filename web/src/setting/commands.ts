import { Effect, Match, Schema } from "effect";
import { Command } from "foldkit";
import {
  clearTaskImplementer,
  clearWorkstreamImplementer,
  clearWorkstreamLead,
  listHarnesses,
  setTaskImplementer,
  setWorkstreamImplementer,
  setWorkstreamLead,
} from "@/api/api.gen";
import { answer, request } from "@/lib/effects";
import { bindingText } from "@/lib/harness";
import { showNotice } from "@/lib/notice";
import { Message } from "./message";
import { IssueSetting, Target } from "./schemas";

export const FetchHarnesses = Command.define("FetchHarnesses", {
  messages: [Message.HarnessesLoaded, Message.LoginRequired, Message.HarnessesFailed],
  execute: request(() => listHarnesses()).pipe(
    Effect.map((res) => {
      if (res.status === 401) {
        return Message.LoginRequired();
      }
      if (res.status !== 200) {
        return Message.HarnessesFailed({ error: res.data.error });
      }
      return Message.HarnessesLoaded({ harnesses: res.data.data });
    }),
    Effect.catchTag("RequestFailed", (error) =>
      Effect.succeed(Message.HarnessesFailed({ error: error.message })),
    ),
  ),
});

export const SaveSetting = Command.define("SaveSetting", {
  args: { target: Target, title: Schema.String, setting: IssueSetting },
  messages: [Message.Saved, Message.LoginRequired, Message.Failed],
  execute: ({ target, title, setting }) =>
    answer(
      () =>
        Match.value(target).pipe(
          Match.tag("Task", ({ owner, name, number }) =>
            setTaskImplementer(owner, name, number, setting),
          ),
          Match.tag("Lead", ({ owner, name, number }) =>
            setWorkstreamLead(owner, name, number, setting),
          ),
          Match.tag("WorkstreamImplementer", ({ owner, name, number }) =>
            setWorkstreamImplementer(owner, name, number, setting),
          ),
          Match.exhaustive,
        ),
      {
        done: () => {
          showNotice(`Set the ${title} to ${bindingText(setting)}`);
          return Message.Saved({ setting });
        },
        login: Message.LoginRequired,
        failed: (error) => Message.Failed({ error }),
      },
    ),
});

export const ClearSetting = Command.define("ClearSetting", {
  args: { target: Target, title: Schema.String },
  messages: [Message.Cleared, Message.LoginRequired, Message.Failed],
  execute: ({ target, title }) =>
    answer(
      () =>
        Match.value(target).pipe(
          Match.tag("Task", ({ owner, name, number }) => clearTaskImplementer(owner, name, number)),
          Match.tag("Lead", ({ owner, name, number }) => clearWorkstreamLead(owner, name, number)),
          Match.tag("WorkstreamImplementer", ({ owner, name, number }) =>
            clearWorkstreamImplementer(owner, name, number),
          ),
          Match.exhaustive,
        ),
      {
        done: () => {
          showNotice(`Cleared the setting of the ${title}`);
          return Message.Cleared();
        },
        login: Message.LoginRequired,
        failed: (error) => Message.Failed({ error }),
      },
    ),
});

import { Effect, Option, Schema } from "effect";
import { Command } from "foldkit";
import { getTranscript, startCheck } from "@/api/api.gen";
import { answer, request } from "@/lib/effects";
import { Message } from "./message";

const fetchPage = (id: number, before?: number) => request(() => getTranscript(id, { before }));

// The first load gets the newest page. A load after a reconnect gets pages until one has the last line that the log got
// before.
export const FetchLog = Command.define("FetchLog", {
  args: { id: Schema.Number, known: Schema.Option(Schema.Number) },
  messages: [Message.Loaded, Message.LoginRequired, Message.LoadFailed],
  execute: ({ id, known }) =>
    Effect.gen(function* () {
      const newest = yield* fetchPage(id);
      if (newest.status === 401) {
        return Message.LoginRequired();
      }
      if (newest.status !== 200) {
        return Message.LoadFailed({ error: newest.data.error });
      }
      const pages = [newest.data.data];
      if (Option.isSome(known)) {
        while (pages[0].older && pages[0].lines[0].id > known.value) {
          const page = yield* fetchPage(id, pages[0].lines[0].id);
          if (page.status === 401) {
            return Message.LoginRequired();
          }
          if (page.status !== 200) {
            return Message.LoadFailed({ error: page.data.error });
          }
          pages.unshift(page.data.data);
        }
      }
      return Message.Loaded({
        lines: pages.flatMap((page) => page.lines),
        older: newest.data.data.older,
        initial: Option.isNone(known),
      });
    }).pipe(
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.LoadFailed({ error: error.message })),
      ),
    ),
});

export const FetchOlder = Command.define("FetchOlder", {
  args: { id: Schema.Number, before: Schema.Number },
  messages: [Message.OlderLoaded, Message.LoginRequired, Message.LoadFailed],
  execute: ({ id, before }) =>
    fetchPage(id, before).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status !== 200) {
          return Message.LoadFailed({ error: res.data.error });
        }
        return Message.OlderLoaded({ lines: res.data.data.lines, older: res.data.data.older });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.LoadFailed({ error: error.message })),
      ),
    ),
});

export const StartCheck = Command.define("StartCheck", {
  args: { id: Schema.Number },
  messages: [Message.StartedCheck, Message.LoginRequired, Message.StartCheckFailed],
  execute: ({ id }) =>
    answer(() => startCheck(id), {
      done: Message.StartedCheck,
      login: Message.LoginRequired,
      failed: (error) => Message.StartCheckFailed({ error }),
    }),
});

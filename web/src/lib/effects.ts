import { Effect, Schema } from "effect";
import type { Reply } from "./harness";

export class RequestFailed extends Schema.TaggedError<RequestFailed>()("RequestFailed", {
  message: Schema.String,
}) {}

export class StoreFailed extends Schema.TaggedError<StoreFailed>()("StoreFailed", {
  message: Schema.String,
}) {}

export class ImageFailed extends Schema.TaggedError<ImageFailed>()("ImageFailed", {
  message: Schema.String,
}) {}

export const request = <A>(run: (signal: AbortSignal) => Promise<A>) =>
  Effect.tryPromise({ try: run, catch: (cause) => new RequestFailed({ message: String(cause) }) });

export const store = <A>(run: () => Promise<A>) =>
  Effect.tryPromise({ try: run, catch: (cause) => new StoreFailed({ message: String(cause) }) });

export const fitted = <A>(run: () => Promise<A>) =>
  Effect.tryPromise({
    try: run,
    catch: (cause) =>
      new ImageFailed({ message: cause instanceof Error ? cause.message : String(cause) }),
  });

// Turns an answer with no body into a Message: 204 is done, 401 asks for the login when the screen has login, and any
// other status has the reason.
export const answer = <Done, Failed, Login = never>(
  run: () => Promise<Reply>,
  messages: { done: () => Done; login?: () => Login; failed: (error: string) => Failed },
) =>
  request(run).pipe(
    Effect.map((res) => {
      if (res.status === 204) {
        return messages.done();
      }
      return res.status === 401 && messages.login
        ? messages.login()
        : messages.failed(res.data.error);
    }),
    Effect.catchTag("RequestFailed", (error) => Effect.succeed(messages.failed(error.message))),
  );

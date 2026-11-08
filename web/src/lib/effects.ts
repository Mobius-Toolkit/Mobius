import { Effect, Schema } from "effect";

export class RequestFailed extends Schema.TaggedError<RequestFailed>()("RequestFailed", {
  message: Schema.String,
}) {}

export class StoreFailed extends Schema.TaggedError<StoreFailed>()("StoreFailed", {
  message: Schema.String,
}) {}

export class VoiceFailed extends Schema.TaggedError<VoiceFailed>()("VoiceFailed", {
  message: Schema.String,
}) {}

export class MarkdownFailed extends Schema.TaggedError<MarkdownFailed>()("MarkdownFailed", {
  message: Schema.String,
}) {}

export class ImageFailed extends Schema.TaggedError<ImageFailed>()("ImageFailed", {
  message: Schema.String,
}) {}

export const request = <A>(run: (signal: AbortSignal) => Promise<A>) =>
  Effect.tryPromise({ try: run, catch: (cause) => new RequestFailed({ message: String(cause) }) });

export const store = <A>(run: () => Promise<A>) =>
  Effect.tryPromise({ try: run, catch: (cause) => new StoreFailed({ message: String(cause) }) });

export const voice = <A>(run: () => Promise<A>) =>
  Effect.tryPromise({ try: run, catch: (cause) => new VoiceFailed({ message: String(cause) }) });

export const fitted = <A>(run: () => Promise<A>) =>
  Effect.tryPromise({
    try: run,
    catch: (cause) =>
      new ImageFailed({ message: cause instanceof Error ? cause.message : String(cause) }),
  });

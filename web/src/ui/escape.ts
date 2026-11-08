import { Effect, Queue, Stream } from "effect";

// Gives message each time that the Owner presses Escape.
export function escapeKey<Message>(message: Message) {
  return Stream.callback<Message>((queue) => {
    const listener = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        Queue.offerUnsafe(queue, message);
      }
    };
    return Effect.gen(function* () {
      window.addEventListener("keydown", listener);
      yield* Effect.addFinalizer(() =>
        Effect.sync(() => window.removeEventListener("keydown", listener)),
      );
    });
  });
}

import { Effect, Queue, Stream } from "effect";

// Gives message each time that the Owner presses Escape. The listener runs in the capture phase of window and stops the
// event, so the Radix layer below the layer of the caller does not close with it.
export function escapeKey<Message>(message: Message) {
  return Stream.callback<Message>((queue) => {
    const listener = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.stopPropagation();
        Queue.offerUnsafe(queue, message);
      }
    };
    return Effect.gen(function* () {
      window.addEventListener("keydown", listener, true);
      yield* Effect.addFinalizer(() =>
        Effect.sync(() => window.removeEventListener("keydown", listener, true)),
      );
    });
  });
}

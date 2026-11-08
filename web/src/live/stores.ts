import { Effect, Queue, Stream } from "effect";

// Gives the value of a plain store of web/src/lib to the program: first its current value, then each new value.
export function storeValues<Value, Message>(
  subscribe: (listener: () => void) => () => unknown,
  current: () => Value,
  toMessage: (value: Value) => Message,
) {
  return Stream.callback<Message>((queue) =>
    Effect.gen(function* () {
      let last: Value | undefined;
      const emit = () => {
        const value = current();
        if (value !== last) {
          last = value;
          Queue.offerUnsafe(queue, toMessage(value));
        }
      };
      emit();
      const unsubscribe = subscribe(emit);
      yield* Effect.addFinalizer(() =>
        Effect.sync(() => {
          unsubscribe();
        }),
      );
    }),
  );
}

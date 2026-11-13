import { Effect, Schema } from "effect";

export const retryDelay = 3000;
export const startLimit = 3000;

// Calls the listener with the data of each event of the name. The data must decode with the schema. An event with data
// that does not decode gets a warning and does not call the listener.
export function onEvent<Value>(
  source: EventTarget,
  name: string,
  schema: Schema.Decoder<Value>,
  listener: (data: Value) => void,
) {
  const decode = Schema.decodeUnknownEffect(Schema.fromJsonString(schema));
  const handle = (event: Event) => {
    if (!(event instanceof MessageEvent) || typeof event.data !== "string") {
      return;
    }
    Effect.runFork(
      decode(event.data).pipe(
        Effect.flatMap((data) => Effect.sync(() => listener(data))),
        Effect.catchTag("SchemaError", (error) =>
          Effect.logWarning(`The live event ${name} has no valid data`, error.message),
        ),
      ),
    );
  };
  source.addEventListener(name, handle);
  return () => source.removeEventListener(name, handle);
}

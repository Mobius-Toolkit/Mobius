import { Effect, Queue, Schema, Stream } from "effect";
import { listGitHubApps } from "@/api/api.gen";
import { request } from "@/lib/effects";
import { retryDelay } from "@/lib/events";

const silenceLimit = 35_000;

export type LiveListener<Message> = {
  name: string;
  handle: (data: string) => Effect.Effect<Message | null, Schema.SchemaError>;
};

// Listens to the live event name. The data of the event must decode with the schema, and toMessage can give null for an
// event that the screen does not need.
export function liveListener<Value, Message>(
  name: string,
  schema: Schema.Decoder<Value>,
  toMessage: (value: Value) => Message | null,
): LiveListener<Message> {
  const decode = Schema.decodeUnknownEffect(Schema.fromJsonString(schema));
  return { name, handle: (data) => decode(data).pipe(Effect.map(toMessage)) };
}

export function listen<Message>(
  queue: Queue.Enqueue<Message>,
  source: EventSource,
  listener: LiveListener<Message>,
) {
  source.addEventListener(listener.name, (event) => {
    if (!(event instanceof MessageEvent) || typeof event.data !== "string") {
      return;
    }
    Effect.runFork(
      listener.handle(event.data).pipe(
        Effect.flatMap((message) =>
          Effect.sync(() => {
            if (message !== null) {
              Queue.offerUnsafe(queue, message);
            }
          }),
        ),
        Effect.catchTag("SchemaError", (error) =>
          Effect.logWarning(`The live event ${listener.name} has no valid data`, error.message),
        ),
      ),
    );
  });
}

// The connection to /api/events, with the reconnect rules of the shell:
// - The server sends a ping each 15 s. A connection that is dead gives no error, so the stream connects again when no
//   ping comes.
// - The browser connects again by itself only while the source is CONNECTING. A source that is CLOSED has an answer that
//   is not an event stream, for example from a proxy. The stream cannot read the status of that answer, so it asks the
//   server for the GitHub Apps: a 401 gives loginRequired, and any other answer connects again.
// - A phone that stops the page in the background can leave a live connection open with no error. Thus the stream
//   connects again when the page is visible again or when the browser is online again.
export function liveEvents<Message>(options: {
  opened: Message;
  loginRequired: Message;
  listeners: ReadonlyArray<LiveListener<Message>>;
}) {
  return Stream.callback<Message>(
    (queue) =>
      Effect.gen(function* () {
        let source: EventSource | undefined;
        let timer: ReturnType<typeof setTimeout> | undefined;
        let stopped = false;

        const schedule = (action: () => void, delay: number) => {
          clearTimeout(timer);
          timer = setTimeout(action, delay);
        };

        const reconnect = () => {
          source?.close();
          connect();
        };

        const watch = () => schedule(reconnect, silenceLimit);

        const reconnectUnlessStopped = Effect.sync(() => {
          if (!stopped) {
            reconnect();
          }
        });

        const retry = () => {
          Effect.runFork(
            request(() => listGitHubApps()).pipe(
              Effect.flatMap((res) =>
                res.status === 401
                  ? Effect.sync(() => Queue.offerUnsafe(queue, options.loginRequired))
                  : reconnectUnlessStopped,
              ),
              Effect.catchTag("RequestFailed", () => reconnectUnlessStopped),
            ),
          );
        };

        const connect = () => {
          const next = new EventSource("/api/events");
          source = next;
          watch();
          next.addEventListener("open", () => {
            watch();
            Queue.offerUnsafe(queue, options.opened);
          });
          next.addEventListener("error", () => {
            if (next.readyState === EventSource.CLOSED) {
              schedule(retry, retryDelay);
            }
          });
          next.addEventListener("ping", watch);
          options.listeners.forEach((listener) => listen(queue, next, listener));
        };

        const wake = () => {
          if (!document.hidden) {
            reconnect();
          }
        };

        const restore = (event: PageTransitionEvent) => {
          if (event.persisted) {
            reconnect();
          }
        };

        window.addEventListener("online", wake);
        window.addEventListener("pageshow", restore);
        document.addEventListener("visibilitychange", wake);

        connect();

        yield* Effect.addFinalizer(() =>
          Effect.sync(() => {
            stopped = true;
            clearTimeout(timer);
            window.removeEventListener("online", wake);
            window.removeEventListener("pageshow", restore);
            document.removeEventListener("visibilitychange", wake);
            source?.close();
          }),
        );
      }),
    { bufferSize: 1000 },
  );
}

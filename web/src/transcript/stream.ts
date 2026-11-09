import { Effect, Queue, Stream } from "effect";
import { retryDelay } from "@/lib/events";
import { listen, liveListener } from "@/live/events";
import { Message } from "./message";
import { Line } from "./schemas";

// The stream of the lines of one agent. The browser connects again by itself only while the source is CONNECTING. A
// source that is CLOSED has an answer that is not an event stream, so the stream asks for a new read of the log and
// connects again after a delay. A phone that stops the page in the background can leave a connection open with no
// error, so the stream also connects again when the page is visible again or when the browser is online again.
export function transcriptStream(id: number) {
  return Stream.callback<Message>((queue) =>
    Effect.gen(function* () {
      let source: EventSource | undefined;
      let timer: ReturnType<typeof setTimeout> | undefined;

      const connect = () => {
        const next = new EventSource(`/api/agents/${id}/transcript/events`);
        source = next;
        next.addEventListener("open", () => Queue.offerUnsafe(queue, Message.StreamOpened()));
        next.addEventListener("error", () => {
          Queue.offerUnsafe(queue, Message.StreamClosed());
          if (next.readyState === EventSource.CLOSED) {
            clearTimeout(timer);
            timer = setTimeout(() => {
              Queue.offerUnsafe(queue, Message.StreamRetried());
              connect();
            }, retryDelay);
          }
        });
        listen(
          queue,
          next,
          liveListener("transcript", Line, (line) => Message.LineReceived({ line })),
        );
      };

      const reconnect = () => {
        clearTimeout(timer);
        source?.close();
        connect();
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
          clearTimeout(timer);
          window.removeEventListener("online", wake);
          window.removeEventListener("pageshow", restore);
          document.removeEventListener("visibilitychange", wake);
          source?.close();
        }),
      );
    }),
  );
}

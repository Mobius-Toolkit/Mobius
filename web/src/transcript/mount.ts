import { Effect, Queue, Stream } from "effect";
import { Mount } from "foldkit";
import { atEnd } from "@/lib/scroll";
import { Message } from "./message";

type Place = { id: string; offset: number };

// The log stays at its end while the Owner is at the end. New lines while the Owner is away from the end show the button
// of the view. A line before the first line keeps the place of the line that the Owner sees. The view sets data-version
// when the lines change, and data-jumps when the Owner asks for the end.
export const TranscriptLog = Mount.defineStream("TranscriptLog", {
  messages: [Message.NearTop, Message.ArrivedWhileAway, Message.ReachedEnd],
  execute: ({ element }) =>
    Stream.callback<
      | ReturnType<typeof Message.NearTop>
      | ReturnType<typeof Message.ArrivedWhileAway>
      | ReturnType<typeof Message.ReachedEnd>
    >((queue) =>
      Effect.gen(function* () {
        const list = element instanceof HTMLElement ? element : null;
        if (list === null) {
          return;
        }
        let pinned = true;
        let version = list.dataset.version;
        let jumps = list.dataset.jumps;
        let place: Place | undefined;

        const firstId = () => list.querySelector("[data-line]")?.getAttribute("data-line") ?? "";

        let first = firstId();

        const nearTop = () => {
          if (list.children.length > 0 && list.scrollTop < list.clientHeight) {
            Queue.offerUnsafe(queue, Message.NearTop());
          }
        };

        const keepPlace = () => {
          const top = list.getBoundingClientRect().top;
          for (const item of list.querySelectorAll("[data-line]")) {
            const rect = item.getBoundingClientRect();
            if (rect.bottom > top) {
              place = { id: item.getAttribute("data-line") ?? "", offset: rect.top - top };
              return;
            }
          }
        };

        const restorePlace = () => {
          if (!place) {
            return;
          }
          const item = list.querySelector(`[data-line="${place.id}"]`);
          if (item) {
            list.scrollTop +=
              item.getBoundingClientRect().top - list.getBoundingClientRect().top - place.offset;
          }
        };

        const onScroll = () => {
          const wasPinned = pinned;
          pinned = atEnd(list);
          keepPlace();
          if (pinned && !wasPinned) {
            Queue.offerUnsafe(queue, Message.ReachedEnd());
          }
          nearTop();
        };

        const layout = () => {
          if (list.dataset.jumps !== jumps) {
            jumps = list.dataset.jumps;
            pinned = true;
            list.scrollTop = list.scrollHeight;
          }
          if (list.dataset.version !== version) {
            version = list.dataset.version;
            const nextFirst = firstId();
            const prepended = first !== "" && nextFirst !== "" && Number(nextFirst) < Number(first);
            first = nextFirst;
            if (prepended) {
              restorePlace();
            } else if (pinned) {
              list.scrollTop = list.scrollHeight;
            } else {
              Queue.offerUnsafe(queue, Message.ArrivedWhileAway());
            }
            keepPlace();
            nearTop();
          }
        };

        list.addEventListener("scroll", onScroll);
        const mutations = new MutationObserver(layout);
        mutations.observe(list, {
          attributes: true,
          attributeFilter: ["data-version", "data-jumps"],
        });
        layout();

        yield* Effect.addFinalizer(() =>
          Effect.sync(() => {
            list.removeEventListener("scroll", onScroll);
            mutations.disconnect();
          }),
        );
      }),
    ),
});

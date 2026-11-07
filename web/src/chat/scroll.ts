import { Effect, Queue, Stream } from "effect";
import { Mount } from "foldkit";
import { atEnd } from "@/lib/scroll";
import { Message } from "./message";

type Place = { id: string; offset: number };

// The list stays at its end while the Owner is at the end. The first scroll waits until the view marks the list as
// ready, and it goes to the first unread message. A message that the list gets before its first message keeps the
// place of the message that the Owner sees.
export const ChatList = Mount.defineStream("ChatList", {
  messages: [Message.ListNearTop],
  execute: ({ element }) =>
    Stream.callback<ReturnType<typeof Message.ListNearTop>>((queue) =>
      Effect.gen(function* () {
        const list = element instanceof HTMLElement ? element : null;
        if (list === null) {
          return;
        }
        let pinned = true;
        let endTop: number | undefined;
        let scrolled = false;
        let count = 0;
        let firstId: number | undefined;
        let place: Place | undefined;

        const scrollToEnd = () => {
          list.scrollTop = list.scrollHeight;
          endTop = list.scrollTop;
        };

        const nearTop = () => {
          if (scrolled && list.scrollTop < list.clientHeight) {
            Queue.offerUnsafe(queue, Message.ListNearTop());
          }
        };

        const keepPlace = () => {
          if (!scrolled) {
            return;
          }
          const top = list.getBoundingClientRect().top;
          for (const item of list.querySelectorAll("[data-message]")) {
            const rect = item.getBoundingClientRect();
            if (rect.bottom > top) {
              place = { id: item.getAttribute("data-message") ?? "", offset: rect.top - top };
              return;
            }
          }
        };

        const restorePlace = () => {
          if (!place) {
            return;
          }
          const item = list.querySelector(`[data-message="${place.id}"]`);
          if (item) {
            list.scrollTop +=
              item.getBoundingClientRect().top - list.getBoundingClientRect().top - place.offset;
          }
        };

        // The end of the list moves when a message grows after its first layout, for example a table or a font.
        const resize = new ResizeObserver(() => {
          if (pinned) {
            scrollToEnd();
          }
        });

        const layout = () => {
          // The toggle of the Brief bubble must not move the list.
          for (const child of list.children) {
            if (child.tagName !== "SECTION") {
              resize.observe(child);
            }
          }
          const items = list.querySelectorAll("[data-message], [data-queued]");
          const messages = list.querySelectorAll("[data-message]");
          const nextFirst =
            messages.length > 0 ? Number(messages[0].getAttribute("data-message")) : undefined;
          if (!scrolled) {
            if (list.dataset.ready !== "true") {
              return;
            }
            scrolled = true;
            count = items.length;
            firstId = nextFirst;
            const unread = list.dataset.firstUnread;
            const target = unread ? list.querySelector(`[data-message="${unread}"]`) : null;
            if (target) {
              list.scrollTop +=
                target.getBoundingClientRect().top - list.getBoundingClientRect().top;
              pinned = atEnd(list);
            } else {
              scrollToEnd();
            }
            keepPlace();
            nearTop();
            return;
          }
          const prepended = firstId !== undefined && nextFirst !== undefined && nextFirst < firstId;
          firstId = nextFirst;
          if (prepended || (!pinned && items.length <= count)) {
            restorePlace();
            count = items.length;
          } else {
            if (items.length > count) {
              pinned = true;
            }
            count = items.length;
            if (pinned) {
              scrollToEnd();
            } else {
              restorePlace();
            }
          }
          keepPlace();
          nearTop();
        };

        const onScroll = () => {
          pinned = atEnd(list) || list.scrollTop === endTop;
          keepPlace();
          nearTop();
        };
        // An image has no height before it loads, and its load moves the end of the list.
        const onLoad = () => {
          if (pinned) {
            scrollToEnd();
          }
        };
        list.addEventListener("scroll", onScroll);
        list.addEventListener("load", onLoad, true);
        // The list gets shorter when the input gets taller, and the end of the list must stay in view.
        resize.observe(list);
        const mutations = new MutationObserver(layout);
        mutations.observe(list, {
          childList: true,
          subtree: true,
          attributes: true,
          attributeFilter: ["data-ready", "data-first-unread"],
        });
        layout();

        yield* Effect.addFinalizer(() =>
          Effect.sync(() => {
            list.removeEventListener("scroll", onScroll);
            list.removeEventListener("load", onLoad, true);
            resize.disconnect();
            mutations.disconnect();
          }),
        );
      }),
    ),
});

import { Effect, Option, Schema } from "effect";
import { back } from "foldkit/navigation";

// Each entry of the history that the application writes holds its place. An entry at place 0 is the first page that the
// tab opened, so the page has no earlier page of the application to go back to.
const HistoryState = Schema.Struct({ index: Schema.Number });

const placeOfEntry = () =>
  Option.match(Schema.decodeUnknownOption(HistoryState)(window.history.state), {
    onNone: () => 0,
    onSome: ({ index }) => index,
  });

const notify = () => window.dispatchEvent(new CustomEvent("foldkit:urlchange"));

// The scroll position of each entry stays in the storage of the tab. A page scrolls in #content on a phone and in the
// window on a desktop.
const Position = Schema.Struct({ content: Schema.Number, page: Schema.Number });
const decodePosition = Schema.decodeUnknownOption(Schema.fromJsonString(Position));
const encodePosition = Schema.encodeSync(Schema.fromJsonString(Position));

const positionKey = (index: number) => `scroll-${index}`;

const content = () => document.getElementById("content");

const savePosition = (index: number) => {
  sessionStorage.setItem(
    positionKey(index),
    encodePosition({ content: content()?.scrollTop ?? 0, page: window.scrollY }),
  );
};

let stopRestore = () => {};

// Foldkit renders in the next animation frame, and a list can fill after that. Thus the position of a back navigation
// stays until the page is tall enough, or until the Owner scrolls.
const restorePosition = (index: number) => {
  stopRestore();
  const saved = Option.flatMapNullishOr(
    Option.fromNullishOr(sessionStorage.getItem(positionKey(index))),
    (text) => Option.getOrNull(decodePosition(text)),
  );
  const position = Option.getOrElse(saved, () => ({ content: 0, page: 0 }));
  const apply = () => {
    const element = content();
    if (!element) {
      return false;
    }
    const ready =
      element.scrollHeight - element.clientHeight >= position.content &&
      document.documentElement.scrollHeight - window.innerHeight >= position.page;
    if (ready) {
      element.scrollTop = position.content;
      window.scrollTo({ top: position.page });
    }
    return ready;
  };
  const frame = requestAnimationFrame(() =>
    requestAnimationFrame(() => {
      if (Option.isNone(saved)) {
        apply();
        return;
      }
      if (apply()) {
        return;
      }
      const stop = () => {
        observer.disconnect();
        document.removeEventListener("scroll", stop, true);
        stopRestore = () => {};
      };
      const observer = new MutationObserver(() => {
        if (apply()) {
          stop();
        }
      });
      const element = content();
      if (element) {
        observer.observe(element, { childList: true, subtree: true });
      }
      document.addEventListener("scroll", stop, true);
      stopRestore = stop;
    }),
  );
  stopRestore = () => cancelAnimationFrame(frame);
};

let shownIndex: number | null = null;
let pushed = false;

export const pushUrl = (path: string) =>
  Effect.sync(() => {
    const index = placeOfEntry();
    savePosition(index);
    sessionStorage.removeItem(positionKey(index + 1));
    shownIndex = index;
    pushed = true;
    window.history.pushState({ index: index + 1 }, "", path);
    notify();
  });

export const replaceUrl = (path: string) =>
  Effect.sync(() => {
    window.history.replaceState({ index: placeOfEntry() }, "", path);
    notify();
  });

export const goBackOr = (parent: string) =>
  Effect.suspend(() => (placeOfEntry() > 0 ? back() : replaceUrl(parent)));

// A new page starts at the top, and a page that the Owner reaches with Back or Forward gets its position again.
export const settleScroll = Effect.sync(() => {
  const index = placeOfEntry();
  if (index === shownIndex && !pushed) {
    return;
  }
  if (!pushed && shownIndex !== null) {
    savePosition(shownIndex);
  }
  pushed = false;
  shownIndex = index;
  restorePosition(index);
});

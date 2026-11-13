import { Effect } from "effect";
import { listUnread, type Unread } from "@/api/api.gen";
import { ListUnreadResponse } from "@/api/schemas.gen";
import { request, type RequestFailed } from "./effects";
import { onEvent } from "./events";

const UnreadSchema = ListUnreadResponse.fields.data.value;

export type ChatKey = {
  organization: string;
  repository: string;
  workstream: number;
};

export function sameChat(a: ChatKey, b: ChatKey) {
  return (
    a.organization === b.organization &&
    a.repository === b.repository &&
    a.workstream === b.workstream
  );
}

export function unreadCount(unread: readonly Unread[], chat: ChatKey) {
  return unread.find((other) => sameChat(other, chat))?.count ?? 0;
}

// The list is undefined until the first read of the counts.
let unread: Unread[] | undefined;
const listeners = new Set<() => void>();

export function currentUnread() {
  return unread;
}

export function subscribeUnread(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

function setUnread(next: Unread[]) {
  unread = next;
  listeners.forEach((listener) => listener());
}

// A read that an event overtakes has old counts, so the read starts again.
let changes = 0;

const loadUnread = Effect.fnUntraced(function* (): Effect.fn.Return<void, RequestFailed> {
  const started = changes;
  const res = yield* request(() => listUnread());
  if (changes !== started) {
    return yield* loadUnread();
  }
  if (res.status === 200) {
    setUnread(res.data.data);
  }
}, Effect.ignore);

const reloadUnread = () => {
  Effect.runFork(loadUnread());
};

export function startUnread(source: EventSource) {
  // The server sends an unread event only at a change, so each connection reads the counts.
  reloadUnread();
  source.addEventListener("open", reloadUnread);
  const remove = onEvent(source, "unread", UnreadSchema, (count) => {
    changes++;
    setUnread([...(unread ?? []).filter((other) => !sameChat(other, count)), count]);
  });
  return () => {
    source.removeEventListener("open", reloadUnread);
    remove();
  };
}

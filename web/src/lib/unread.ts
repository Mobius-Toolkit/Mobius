import { listUnread, type LiveEvents, type Unread } from "@/api/api.gen";
import { onEvent } from "./events";

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

function loadUnread() {
  listUnread()
    .then((res) => {
      if (res.status === 200) {
        setUnread(res.data.data);
      }
    })
    .catch(() => {});
}

export function startUnread(source: EventSource) {
  // The server sends an unread event only at a change, so each connection reads the counts.
  loadUnread();
  source.addEventListener("open", loadUnread);
  const remove = onEvent<LiveEvents, "unread">(source, "unread", (count) =>
    setUnread([...(unread ?? []).filter((other) => !sameChat(other, count)), count]),
  );
  return () => {
    source.removeEventListener("open", loadUnread);
    remove();
  };
}

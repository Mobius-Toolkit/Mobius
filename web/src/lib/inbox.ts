import {
  listInbox,
  type InboxItem,
  type LiveEvents,
  type NeedsHuman,
  type PendingAction,
} from "@/api/api.gen";
import { onEvent } from "./events";

const answeredLimit = 2 * 60 * 1000;

// An item that the Owner answered or decided. The server closes it at the next poll, so until a read without the
// item (item) or without its issue (mark), or until the limit, the UI hides the item and the mark of its issue.
export type Answered = {
  id: number;
  repository: string;
  issue: number;
  time: number;
  item: boolean;
  mark: boolean;
};

export type InboxState = {
  items: InboxItem[];
  answered: Answered[];
};

let state: InboxState = { items: [], answered: [] };
const listeners = new Set<() => void>();
const answeredTimers = new Map<number, ReturnType<typeof setTimeout>>();

function notify() {
  listeners.forEach((listener) => listener());
}

function setAnswered(update: (list: Answered[]) => Answered[]) {
  const next = update(state.answered);
  state = { ...state, answered: next };
  for (const item of next) {
    if (!answeredTimers.has(item.id)) {
      const remaining = Math.max(0, item.time + answeredLimit - Date.now());
      answeredTimers.set(
        item.id,
        setTimeout(() => {
          answeredTimers.delete(item.id);
          setAnswered((list) => list.filter((one) => one.id !== item.id));
        }, remaining),
      );
    }
  }
  notify();
}

export function hideInboxItem(item: { id: number; repository: string; issue: number }) {
  setAnswered((list) => [
    ...list.filter((other) => other.id !== item.id),
    {
      id: item.id,
      repository: item.repository,
      issue: item.issue,
      time: Date.now(),
      item: true,
      mark: true,
    },
  ]);
}

// Brings the item back after a failed request. The caller shows the error itself, so the shell keeps no failure.
export function restoreInboxItem(id: number) {
  const timer = answeredTimers.get(id);
  if (timer) {
    clearTimeout(timer);
    answeredTimers.delete(id);
  }
  setAnswered((list) => list.filter((other) => other.id !== id));
}

export function currentInbox(): InboxState {
  return state;
}

export function subscribeInbox(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function openInboxItems(inbox: InboxState): InboxItem[] {
  return inbox.items.filter(
    (item) => !item.merging && !inbox.answered.some((other) => other.item && other.id === item.id),
  );
}

// A new list ends the mark of an issue that the list no longer holds, and an entry with no item and no mark goes away.
export function dropMarks(needsHuman: readonly NeedsHuman[]) {
  setAnswered((list) =>
    list
      .map((other) => ({
        ...other,
        mark: other.mark && needsHuman.some((row) => isIssueOf(other, row)),
      }))
      .filter((other) => other.item || other.mark),
  );
}

export function filterNeedsHuman(
  needsHuman: readonly NeedsHuman[],
  answered: readonly Answered[],
): NeedsHuman[] {
  return needsHuman.filter((row) => !answered.some((other) => other.mark && isIssueOf(other, row)));
}

export function isIssueOf(answered: Answered, row: NeedsHuman) {
  return row.repository === answered.repository && row.number === answered.issue;
}

let changes = 0;

export function loadInbox(): Promise<void> {
  const started = changes;
  return listInbox()
    .then((res): Promise<void> | void => {
      if (changes !== started) {
        return loadInbox();
      }
      if (res.status === 200) {
        const read = res.data.data;
        const nextAnswered = state.answered
          .map((other) => ({
            ...other,
            item: other.item && read.some((item) => item.id === other.id),
          }))
          .filter((other) => other.item || other.mark);
        state = { items: read, answered: nextAnswered };
        notify();
      }
    })
    .catch(() => {});
}

export function updateInboxAction(action: PendingAction) {
  state = {
    ...state,
    items: state.items.map((item) => (item.action?.id === action.id ? { ...item, action } : item)),
  };
  notify();
}

export function startInbox(source: EventSource) {
  void loadInbox();
  source.addEventListener("open", loadInbox);
  const remove = onEvent<LiveEvents, "inbox">(source, "inbox", (item) => {
    changes++;
    setAnswered((list) => list.filter((other) => other.id !== item.id));
    const others = state.items.filter((other) => other.id !== item.id);
    state = {
      ...state,
      items: item.dismissedAt ? others : [...others, item],
    };
    notify();
  });
  const removeAction = onEvent<LiveEvents, "action">(source, "action", (action) => {
    changes++;
    updateInboxAction(action);
  });
  return () => {
    source.removeEventListener("open", loadInbox);
    remove();
    removeAction();
  };
}

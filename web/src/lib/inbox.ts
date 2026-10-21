import { useCallback, useEffect, useMemo, useState } from "react";
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
type Answered = {
  id: number;
  repository: string;
  issue: number;
  time: number;
  item: boolean;
  mark: boolean;
};

export function useInbox(source: EventSource | undefined, needsHuman: NeedsHuman[]) {
  const [items, setItems] = useState<InboxItem[]>([]);
  const [answered, setAnswered] = useState<Answered[]>([]);
  const [failure, setFailure] = useState<{ id: number; error: string }>();

  const hide = useCallback((item: InboxItem) => {
    setFailure(undefined);
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
  }, []);

  const fail = useCallback((id: number, error: string) => {
    setAnswered((list) => list.filter((other) => other.id !== id));
    setFailure({ id, error });
  }, []);

  const clearFailure = useCallback(() => setFailure(undefined), []);

  const updateAction = useCallback(
    (action: PendingAction) =>
      setItems((list) =>
        list.map((item) => (item.action?.id === action.id ? { ...item, action } : item)),
      ),
    [],
  );

  useEffect(() => {
    if (!source) {
      return;
    }
    // An Inbox event that comes while the connection is down is lost, so each connection reads the items.
    const load = () => {
      listInbox()
        .then((res) => {
          if (res.status === 200) {
            const read = res.data.data;
            setItems(read);
            setAnswered((list) =>
              list
                .map((other) => ({
                  ...other,
                  item: other.item && read.some((item) => item.id === other.id),
                }))
                .filter((other) => other.item || other.mark),
            );
          }
        })
        .catch(() => {});
    };
    load();
    source.addEventListener("open", load);
    const remove = onEvent<LiveEvents, "inbox">(source, "inbox", (item) => {
      setAnswered((list) => list.filter((other) => other.id !== item.id));
      setItems((list) => {
        const others = list.filter((other) => other.id !== item.id);
        return item.dismissedAt ? others : [...others, item];
      });
    });
    const removeAction = onEvent<LiveEvents, "action">(source, "action", updateAction);
    return () => {
      source.removeEventListener("open", load);
      remove();
      removeAction();
    };
  }, [source, updateAction]);

  const [readNeedsHuman, setReadNeedsHuman] = useState(needsHuman);
  if (readNeedsHuman !== needsHuman) {
    setReadNeedsHuman(needsHuman);
    setAnswered((list) =>
      list
        .map((other) => ({
          ...other,
          mark: other.mark && needsHuman.some((row) => isIssueOf(other, row)),
        }))
        .filter((other) => other.item || other.mark),
    );
  }

  useEffect(() => {
    const timers = answered.map((other) =>
      setTimeout(
        () => setAnswered((list) => list.filter((one) => one.id !== other.id)),
        Math.max(0, other.time + answeredLimit - Date.now()),
      ),
    );
    return () => timers.forEach(clearTimeout);
  }, [answered]);

  // A merging item waits for the answer of GitHub. It comes back with its reason when GitHub refuses the merge.
  const open = useMemo(
    () =>
      items.filter(
        (item) => !item.merging && !answered.some((other) => other.item && other.id === item.id),
      ),
    [items, answered],
  );

  const marked = useMemo(
    () =>
      needsHuman.filter((row) => !answered.some((other) => other.mark && isIssueOf(other, row))),
    [needsHuman, answered],
  );

  const leave = (id: number) => setItems((list) => list.filter((item) => item.id !== id));

  return {
    items: open,
    needsHuman: marked,
    leave,
    updateAction,
    hide,
    fail,
    failure,
    clearFailure,
  };
}

function isIssueOf(answered: Answered, row: NeedsHuman) {
  return row.repository === answered.repository && row.number === answered.issue;
}

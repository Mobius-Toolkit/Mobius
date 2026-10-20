import { useEffect, useMemo, useState } from "react";
import { listInbox, type LiveEvents } from "@/api/api.gen";
import type { ActionItem } from "./actions";
import { onEvent } from "./events";

export function useInbox(source?: EventSource) {
  const [items, setItems] = useState<ActionItem[]>([]);

  useEffect(() => {
    if (!source) {
      return;
    }
    // An Inbox event that comes while the connection is down is lost, so each connection reads the items.
    const load = () => {
      listInbox()
        .then((res) => {
          if (res.status === 200) {
            setItems(res.data.data);
          }
        })
        .catch(() => {});
    };
    load();
    source.addEventListener("open", load);
    const remove = onEvent<LiveEvents, "inbox">(source, "inbox", (item) =>
      setItems((list) => {
        const others = list.filter((other) => other.id !== item.id);
        return item.dismissedAt ? others : [...others, item];
      }),
    );
    return () => {
      source.removeEventListener("open", load);
      remove();
    };
  }, [source]);

  // A merging item waits for the answer of GitHub. It comes back with its reason when GitHub refuses the merge.
  const open = useMemo(() => items.filter((item) => !item.merging), [items]);

  const leave = (id: number) => setItems((list) => list.filter((item) => item.id !== id));

  return { items: open, leave };
}

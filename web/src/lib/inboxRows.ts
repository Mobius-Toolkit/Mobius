import { InboxItemKind } from "@/api/api.gen";
import type { InboxItem } from "@/chat/schemas";

export type InboxRow = { item: InboxItem; todos: InboxItem[] };

const pullRequestKey = (item: InboxItem) => `${item.repository}#${item.pullRequest}`;

// A row has the item "ready for review" of a pull request and the todo items of the pull request. The todo items of a
// pull request with no such item make one row, and its item is the oldest todo item.
export function inboxRows(items: readonly InboxItem[]): InboxRow[] {
  const sorted = items.toSorted((a, b) => a.id - b.id);
  const todosOf = new Map<string, InboxItem[]>();
  for (const item of sorted) {
    if (item.kind === InboxItemKind.todo) {
      todosOf.set(pullRequestKey(item), [...(todosOf.get(pullRequestKey(item)) ?? []), item]);
    }
  }
  const rows: InboxRow[] = [];
  for (const item of sorted) {
    if (item.kind === InboxItemKind.ready_for_review) {
      const key = pullRequestKey(item);
      rows.push({ item, todos: todosOf.get(key) ?? [] });
      todosOf.delete(key);
    } else if (item.kind !== InboxItemKind.todo) {
      rows.push({ item, todos: [] });
    }
  }
  for (const todos of todosOf.values()) {
    rows.push({ item: todos[0], todos });
  }
  return rows.toSorted((a, b) => a.item.id - b.item.id);
}

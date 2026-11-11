import { InboxItemKind } from "@/api/api.gen";
import type { InboxItem } from "./model";

export const coordinatorTrayItems = (items: readonly InboxItem[], organization: string) =>
  items.filter(
    (item) =>
      item.organization === organization &&
      (item.action
        ? item.action.role === "coordinator"
        : (item.kind === InboxItemKind.question && !item.repository && !item.issue) ||
          (item.kind === InboxItemKind.Lead_failed && item.workstream === 0 && !item.repository)),
  );

export const workstreamTrayItems = (
  items: readonly InboxItem[],
  repository: string,
  workstream: number,
) =>
  items
    .filter((item) => item.workstreamRepository === repository && item.workstream === workstream)
    .toSorted((a, b) => a.id - b.id);

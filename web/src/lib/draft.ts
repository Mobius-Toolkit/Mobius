import { useEffect, useSyncExternalStore } from "react";
import type { ChatInboxItem } from "@/api/api.gen";
import { run } from "./database";

export type DraftInboxItem = ChatInboxItem & { id: number };

export type Draft = { text: string; images: File[]; inboxItems: DraftInboxItem[] };

export const emptyDraft: Draft = { text: "", images: [], inboxItems: [] };

export function chatDraftKey(organization: string, repository: string, workstream: number) {
  return `chatDraft:${organization}/${repository}/${workstream}`;
}

export function answerDraftKey(id: number) {
  return `answerDraft:${id}`;
}

// The drafts that the browser store gave, or that a change made. A change is in this map at once and reaches the
// browser store later, in the order of the changes.
const drafts = new Map<string, Draft>();
const loading = new Map<string, Promise<void>>();
const listeners = new Set<() => void>();

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

function load(key: string) {
  let promise = loading.get(key);
  if (!promise) {
    promise = run<{ key: string } & Draft>("drafts", "readonly", (store) => store.get(key))
      .then((record) => ({
        text: record?.text ?? "",
        images: record?.images ?? [],
        inboxItems: record?.inboxItems ?? [],
      }))
      // The input works with no kept draft.
      .catch(() => emptyDraft)
      .then((draft) => {
        drafts.set(key, draft);
        loading.delete(key);
        listeners.forEach((listener) => listener());
      });
    loading.set(key, promise);
  }
  return promise;
}

export async function loadDraft(key: string) {
  if (!drafts.has(key)) {
    await load(key);
  }
  return drafts.get(key) ?? emptyDraft;
}

// Changes the draft of the key. A draft that the store has not given yet changes after it loads.
export async function updateDraft(key: string, update: (draft: Draft) => Draft) {
  const current = drafts.get(key);
  if (!current) {
    await load(key);
    return updateDraft(key, update);
  }
  const next = update(current);
  drafts.set(key, next);
  listeners.forEach((listener) => listener());
  const empty = !next.text && next.images.length === 0 && next.inboxItems.length === 0;
  // The input works with no kept draft.
  await (
    empty
      ? run("drafts", "readwrite", (store) => store.delete(key))
      : run("drafts", "readwrite", (store) => store.put({ key, ...next }))
  ).catch(() => {});
}

export function saveDraftText(key: string, text: string) {
  return updateDraft(key, (draft) => ({ ...draft, text }));
}

export function addDraftInboxItem(key: string, item: DraftInboxItem) {
  return updateDraft(key, (draft) =>
    draft.inboxItems.some((other) => other.id === item.id)
      ? draft
      : { ...draft, inboxItems: [...draft.inboxItems, item] },
  );
}

// Gives undefined until the store gives the draft.
export function useDraft(key: string) {
  useEffect(() => {
    void load(key);
  }, [key]);
  return useSyncExternalStore(subscribe, () => drafts.get(key));
}

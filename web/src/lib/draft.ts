import type { ChatInboxItem } from "@/api/api.gen";
import { run } from "./database";

export type DraftInboxItem = ChatInboxItem & { id: number };

// The audio stays in the draft until the server gives the text. conversion is the id of the server conversion, or ""
// before the upload ends. error has the text of the failure in the state failed.
export type DraftVoice = {
  state: "converting" | "failed" | "ready";
  audio: Blob | null;
  conversion: string;
  error: string;
};

export type Draft = {
  text: string;
  images: File[];
  inboxItems: DraftInboxItem[];
  voice: DraftVoice | null;
};

export const emptyDraft: Draft = { text: "", images: [], inboxItems: [], voice: null };

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

export function subscribeDrafts(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function currentDraft(key: string) {
  return drafts.get(key);
}

function load(key: string) {
  let promise = loading.get(key);
  if (!promise) {
    promise = run<{ key: string } & Draft>("drafts", "readonly", (store) => store.get(key))
      .then((record) => ({
        text: record?.text ?? "",
        images: record?.images ?? [],
        inboxItems: record?.inboxItems ?? [],
        voice: record?.voice ?? null,
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
  const empty =
    !next.text && next.images.length === 0 && next.inboxItems.length === 0 && !next.voice;
  // The input works with no kept draft.
  await (
    empty
      ? run("drafts", "readwrite", (store) => store.delete(key))
      : run("drafts", "readwrite", (store) => store.put({ key, ...next }))
  ).catch(() => {});
}

// The keys of the stored drafts that have a recording in the state converting.
export async function convertingDraftKeys() {
  const records = await run<({ key: string } & Draft)[]>("drafts", "readonly", (store) =>
    store.getAll(),
  ).catch(() => []);
  return records.filter((record) => record.voice?.state === "converting").map(({ key }) => key);
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

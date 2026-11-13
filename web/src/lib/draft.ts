import { Effect } from "effect";
import type { ChatInboxItem, ChatSelection } from "@/api/api.gen";
import { run } from "./database";
import { store } from "./effects";

export type DraftInboxItem = ChatInboxItem & { id: number | string };

export type DraftSelection = ChatSelection & { id: string };

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
  selections: DraftSelection[];
  voice: DraftVoice | null;
};

export const emptyDraft: Draft = {
  text: "",
  images: [],
  inboxItems: [],
  selections: [],
  voice: null,
};

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
    promise = Effect.runPromise(
      store(() =>
        run<{ key: string } & Draft>("drafts", "readonly", (table) => table.get(key)),
      ).pipe(
        Effect.map((record): Draft => ({
          text: record?.text ?? "",
          images: record?.images ?? [],
          inboxItems: record?.inboxItems ?? [],
          selections: record?.selections ?? [],
          voice: record?.voice ?? null,
        })),
        // The input works with no kept draft.
        Effect.orElseSucceed(() => emptyDraft),
        Effect.map((draft) => {
          drafts.set(key, draft);
          loading.delete(key);
          listeners.forEach((listener) => listener());
        }),
      ),
    );
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
    !next.text &&
    next.images.length === 0 &&
    next.inboxItems.length === 0 &&
    next.selections.length === 0 &&
    !next.voice;
  // The input works with no kept draft.
  await Effect.runPromise(
    store(() =>
      empty
        ? run("drafts", "readwrite", (table) => table.delete(key))
        : run("drafts", "readwrite", (table) => table.put({ key, ...next })),
    ).pipe(Effect.ignore),
  );
}

// The keys of the stored drafts that have a recording in the state converting.
export async function convertingDraftKeys() {
  const records = await Effect.runPromise(
    store(() =>
      run<({ key: string } & Draft)[]>("drafts", "readonly", (table) => table.getAll()),
    ).pipe(Effect.orElseSucceed((): ({ key: string } & Draft)[] => [])),
  );
  return records.filter((record) => record.voice?.state === "converting").map(({ key }) => key);
}

export function saveDraftText(key: string, text: string) {
  return updateDraft(key, (draft) => ({ ...draft, text }));
}

export function addDraftSelection(key: string, selection: DraftSelection) {
  return updateDraft(key, (draft) => ({ ...draft, selections: [...draft.selections, selection] }));
}

export function addDraftInboxItem(key: string, item: DraftInboxItem) {
  return updateDraft(key, (draft) =>
    draft.inboxItems.some((other) => other.id === item.id)
      ? draft
      : { ...draft, inboxItems: [...draft.inboxItems, item] },
  );
}

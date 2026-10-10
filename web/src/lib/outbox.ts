import { getSendChatUrl } from "@/api/api.gen";

// A message of the Owner that the server has not stored. The id is the browser id of the message.
export type Queued = {
  id: string;
  organization: string;
  repository: string;
  workstream: number;
  text: string;
  images: File[];
  time: string;
};

const requestLimit = 60_000;

let database: Promise<IDBDatabase> | undefined;

// One connection keeps the transactions in the order of the calls.
function connect() {
  database ??= new Promise<IDBDatabase>((resolve, reject) => {
    const request = indexedDB.open("mobius", 1);
    request.addEventListener("upgradeneeded", () => {
      request.result.createObjectStore("outbox", { keyPath: "id" }).createIndex("chat", "chat");
    });
    request.addEventListener("success", () => resolve(request.result));
    request.addEventListener("error", () => reject(request.error));
  });
  return database;
}

async function run<T>(mode: IDBTransactionMode, use: (store: IDBObjectStore) => IDBRequest<T>) {
  const db = await connect();
  return new Promise<T>((resolve, reject) => {
    const transaction = db.transaction("outbox", mode);
    const request = use(transaction.objectStore("outbox"));
    transaction.addEventListener("complete", () => resolve(request.result));
    transaction.addEventListener("error", () => reject(transaction.error));
  });
}

const chatKey = (item: Pick<Queued, "organization" | "repository" | "workstream">) => [
  item.organization,
  item.repository,
  item.workstream,
];

export async function listQueued(chat: Pick<Queued, "organization" | "repository" | "workstream">) {
  const items = await run("readonly", (store) => store.index("chat").getAll(chatKey(chat)));
  return (items as Queued[]).toSorted((a, b) => a.time.localeCompare(b.time));
}

export function addQueued(item: Queued) {
  return run("readwrite", (store) => store.add({ ...item, chat: chatKey(item) }));
}

export function removeQueued(id: string) {
  return run("readwrite", (store) => store.delete(id));
}

// Gives undefined when the server stored the message, or the text of the error. The request ends at the limit or
// when the signal aborts.
export async function postQueued(item: Queued, signal: AbortSignal) {
  const form = new FormData();
  form.append("id", item.id);
  form.append("organization", item.organization);
  form.append("repository", item.repository);
  form.append("workstream", String(item.workstream));
  form.append("text", item.text);
  for (const image of item.images) {
    form.append("images", image);
  }
  const res = await fetch(getSendChatUrl(), {
    method: "POST",
    body: form,
    signal: AbortSignal.any([signal, AbortSignal.timeout(requestLimit)]),
  });
  if (res.status === 204) {
    return { status: res.status };
  }
  const body = (await res.json().catch(() => undefined)) as { error?: string } | undefined;
  return { status: res.status, error: body?.error ?? `The server answered ${res.status}.` };
}

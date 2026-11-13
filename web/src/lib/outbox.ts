import { Effect, Option, Schema } from "effect";
import { getSendChatUrl, type ChatInboxItem, type ChatSelection } from "@/api/api.gen";
import { run as runIn } from "./database";
import { request } from "./effects";

// A message of the Owner that the server has not stored. The id is the browser id of the message.
export type Queued = {
  id: string;
  organization: string;
  repository: string;
  workstream: number;
  text: string;
  images: File[];
  inboxItems: ChatInboxItem[];
  selections: ChatSelection[];
  voice: boolean;
  time: string;
};

const requestLimit = 60_000;

const decodeErrorBody = Schema.decodeUnknownEffect(
  Schema.Struct({ error: Schema.optional(Schema.String) }),
);

export function newMessageId() {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
}

const run = <T>(mode: IDBTransactionMode, use: (store: IDBObjectStore) => IDBRequest<T>) =>
  runIn("outbox", mode, use);

const chatKey = (item: Pick<Queued, "organization" | "repository" | "workstream">) => [
  item.organization,
  item.repository,
  item.workstream,
];

let outboxVersion = 0;
const listeners = new Set<() => void>();

export function subscribeOutbox(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function currentOutboxVersion() {
  return outboxVersion;
}

export function listQueued(chat: Pick<Queued, "organization" | "repository" | "workstream">) {
  return run("readonly", (store) => store.index("chat").getAll(chatKey(chat)));
}

export async function addQueued(item: Queued) {
  const result = await run("readwrite", (store) => store.add({ ...item, chat: chatKey(item) }));
  outboxVersion++;
  listeners.forEach((listener) => listener());
  return result;
}

export function addCoordinatorMessage(organization: string, text: string) {
  return addQueued({
    id: newMessageId(),
    organization,
    repository: "",
    workstream: 0,
    text,
    images: [],
    inboxItems: [],
    selections: [],
    voice: false,
    time: new Date().toISOString(),
  });
}

export async function removeQueued(id: string) {
  const result = await run("readwrite", (store) => store.delete(id));
  outboxVersion++;
  listeners.forEach((listener) => listener());
  return result;
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
  form.append("voice", String(item.voice));
  for (const image of item.images) {
    form.append("images", image);
  }
  for (const inboxItem of item.inboxItems) {
    form.append("inboxItems", JSON.stringify(inboxItem));
  }
  for (const selection of item.selections) {
    form.append("selections", JSON.stringify(selection));
  }
  const res = await fetch(getSendChatUrl(), {
    method: "POST",
    body: form,
    signal: AbortSignal.any([signal, AbortSignal.timeout(requestLimit)]),
  });
  if (res.status === 204) {
    return { status: res.status };
  }
  const body = await Effect.runPromise(
    request(() => res.json()).pipe(Effect.flatMap(decodeErrorBody), Effect.option),
  );
  return {
    status: res.status,
    error: Option.flatMapNullishOr(body, (found) => found.error).pipe(
      Option.getOrElse(() => `The server answered ${res.status}.`),
    ),
  };
}

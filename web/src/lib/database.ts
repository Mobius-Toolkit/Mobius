let database: Promise<IDBDatabase> | undefined;

// One connection keeps the transactions in the order of the calls.
function connect() {
  database ??= new Promise<IDBDatabase>((resolve, reject) => {
    const request = indexedDB.open("mobius", 3);
    request.addEventListener("upgradeneeded", () => {
      const db = request.result;
      if (!db.objectStoreNames.contains("outbox")) {
        db.createObjectStore("outbox", { keyPath: "id" }).createIndex("chat", "chat");
      }
      if (!db.objectStoreNames.contains("drafts")) {
        db.createObjectStore("drafts", { keyPath: "key" });
      }
      if (!db.objectStoreNames.contains("voice")) {
        db.createObjectStore("voice", { keyPath: "key" });
      }
    });
    let blocked = false;
    request.addEventListener("blocked", () => {
      blocked = true;
      database = undefined;
      reject(new Error("The database is open in another tab."));
    });
    request.addEventListener("success", () => {
      const db = request.result;
      if (blocked) {
        db.close();
        return;
      }
      db.addEventListener("versionchange", () => {
        db.close();
        database = undefined;
      });
      resolve(db);
    });
    request.addEventListener("error", () => {
      database = undefined;
      reject(request.error);
    });
  });
  return database;
}

export async function run<T>(
  name: "outbox" | "drafts" | "voice",
  mode: IDBTransactionMode,
  use: (store: IDBObjectStore) => IDBRequest<T>,
) {
  const db = await connect();
  return new Promise<T>((resolve, reject) => {
    const transaction = db.transaction(name, mode);
    const request = use(transaction.objectStore(name));
    transaction.addEventListener("complete", () => resolve(request.result));
    transaction.addEventListener("error", () => reject(transaction.error));
    transaction.addEventListener("abort", () => reject(transaction.error));
  });
}

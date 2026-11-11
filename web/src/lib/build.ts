declare const UI_BUILD: string;
declare const UI_RELEASE: string;

export const uiRelease = typeof UI_RELEASE !== "undefined" ? UI_RELEASE : "";

async function newBuild() {
  // The Vite dev server has no ui-version.
  if (import.meta.env.DEV) {
    return false;
  }
  try {
    const response = await fetch("/ui-version", { cache: "no-store" });
    const build = typeof UI_BUILD !== "undefined" ? UI_BUILD : "";
    return response.ok && (await response.text()) !== build;
  } catch {
    return false;
  }
}

let foundNewBuild = false;
const listeners = new Set<() => void>();
let started = false;

function notify() {
  listeners.forEach((listener) => listener());
}

function check() {
  void newBuild().then((differs) => {
    if (differs && !foundNewBuild) {
      foundNewBuild = true;
      notify();
    }
  });
}

function shown() {
  if (!document.hidden) {
    check();
  }
}

export function currentNewBuild(): boolean {
  return foundNewBuild;
}

export function subscribeNewBuild(listener: () => void): () => void {
  listeners.add(listener);
  if (!started) {
    started = true;
    check();
    window.addEventListener("focus", check);
    document.addEventListener("visibilitychange", shown);
    setInterval(check, 5 * 60 * 1000);
  }
  return () => {
    listeners.delete(listener);
  };
}

// The old server answers until it restarts, so only a new build ends the wait.
export async function reloadOnNewBuild(signal: AbortSignal) {
  while (!signal.aborted && !(await newBuild())) {
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  if (!signal.aborted) {
    window.location.reload();
  }
}

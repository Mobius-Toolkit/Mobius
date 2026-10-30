import { useSyncExternalStore } from "react";

const duration = 4000;
const listeners = new Set<() => void>();
let text = "";
let timer: ReturnType<typeof setTimeout> | undefined;

function change(next: string) {
  text = next;
  listeners.forEach((listener) => listener());
}

export function showNotice(next: string) {
  clearTimeout(timer);
  change(next);
  timer = setTimeout(() => change(""), duration);
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function useNotice() {
  return useSyncExternalStore(subscribe, () => text);
}

// A repository other than the home repository is part of the reference.
export function issueRef(repository: string, number: number, home: string) {
  return `${repository === home ? "" : repository}#${number}`;
}

import { useEffect, useSyncExternalStore } from "react";
import { currentDraft, loadDraft, subscribeDrafts } from "./draft";
import { currentUnread, subscribeUnread } from "./unread";
import {
  currentWorkstreams,
  loadWorkstreams,
  subscribeWorkstreams,
  type Workstreams,
} from "./workstreams";

// The list is undefined until the first read of the counts.
export function useUnread() {
  return useSyncExternalStore(subscribeUnread, currentUnread);
}

export function useWorkstreams(): Workstreams {
  const state = useSyncExternalStore(subscribeWorkstreams, currentWorkstreams);
  return { ...state, load: loadWorkstreams };
}

// Gives undefined until the store gives the draft.
export function useDraft(key: string) {
  useEffect(() => {
    void loadDraft(key);
  }, [key]);
  return useSyncExternalStore(subscribeDrafts, () => currentDraft(key));
}

import { createContext, useContext } from "react";
import type {
  Activity,
  GitHubApp,
  InboxItem,
  PendingAction,
  Switches,
  Unread,
} from "@/api/api.gen";
import type { ChangeSwitch } from "./switches";
import type { Workstreams } from "./workstreams";

type Shell = {
  apps: GitHubApp[];
  organizations: string[];
  organization: string;
  selectOrganization: (organization: string) => void;
  source?: EventSource;
  workstreams: Workstreams;
  unread?: Unread[];
  inbox: InboxItem[];
  leaveInbox: (id: number) => void;
  hideInbox: (item: InboxItem) => void;
  failInbox: (id: number, error: string) => void;
  inboxFailure?: { id: number; error: string };
  clearInboxFailure: () => void;
  updateAction: (action: PendingAction) => void;
  activities: Activity[];
  switches?: Switches;
  changeSwitch: ChangeSwitch;
};

export const ShellContext = createContext<Shell | undefined>(undefined);

export function useShell() {
  const shell = useContext(ShellContext);
  if (!shell) {
    throw new Error("The page is not in the shell.");
  }
  return shell;
}

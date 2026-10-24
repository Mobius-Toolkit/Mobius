import type { PendingAction, PendingActionRole } from "@/api/api.gen";

export const roleName: Record<PendingActionRole, string> = {
  coordinator: "Coordinator",
  lead: "Lead",
  triager: "Triager",
};

export type Decision = "approve" | "reject";

export function actionState(action: PendingAction) {
  if (action.state === "pending") {
    return action.error ? "Failed" : "Waits for the approval";
  }
  return {
    done: "Done",
    rejected: "Rejected",
    replaced: "Replaced",
    withdrawn: "Withdrawn by the agent",
  }[action.state];
}

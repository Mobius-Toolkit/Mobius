import type { ChatMessage, InboxItem } from "@/api/api.gen";

export type Role = "coordinator" | "lead" | "triager";

export const roleName: Record<Role, string> = {
  coordinator: "Coordinator",
  lead: "Lead",
  triager: "Triager",
};

export type PendingAction = {
  id: number;
  role: Role;
  tool: string;
  arguments: { name: string; value: string }[];
  state: "pending" | "done" | "rejected" | "replaced";
  // The error of the last failed run. A pending action with an error is a failed action.
  error: string;
  result: string;
  message: string;
  time: string;
  decidedAt?: string;
};

export type ActionMessage = ChatMessage & { action?: PendingAction };
export type ActionItem = InboxItem & { action?: PendingAction };

export type Approval = InboxItem & { action: PendingAction };

export type Decision = "approve" | "reject";

export async function decideAction(id: number, decision: Decision, message: string) {
  const res = await fetch(`/api/actions/${id}/${decision}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ message }),
  });
  const body = (await res.json()) as { data: PendingAction; error: string };
  return { status: res.status, action: body.data, error: body.error };
}

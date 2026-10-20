import type { ChatMessage, InboxItem, PendingAction } from "@/api/api.gen";

export type { PendingAction };

export type Role = PendingAction["role"];

export const roleName: Record<Role, string> = {
  coordinator: "Coordinator",
  lead: "Lead",
  triager: "Triager",
};

export type ActionMessage = ChatMessage;
export type ActionItem = InboxItem;

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

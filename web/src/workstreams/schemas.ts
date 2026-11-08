import { ListNeedsHumanResponse, ListUnreadResponse } from "@/api/schemas.gen";

export { Workstream } from "@/chat/schemas";

export const NeedsHuman = ListNeedsHumanResponse.fields.data.value;
export const Unread = ListUnreadResponse.fields.data.value;

export type NeedsHuman = typeof NeedsHuman.Type;
export type Unread = typeof Unread.Type;

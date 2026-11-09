import { Schema } from "effect";

export const Tab = Schema.Literals(["tasks", "agents", "details", "brief", "recurring"]);

export type Tab = typeof Tab.Type;

import { Schema } from "effect";

export const Tab = Schema.Literals(["tasks", "agents", "details", "brief"]);

export type Tab = typeof Tab.Type;

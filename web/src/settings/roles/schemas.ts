import { Schema } from "effect";
import { ListRolesResponse } from "@/api/schemas.gen";
import { RoleName } from "../schemas";

const ListedRole = ListRolesResponse.fields.data.value;

// The server gives the name of a role as a text, and the request of a role takes only the names of RoleName.
export const Role = Schema.Struct({ ...ListedRole.fields, role: RoleName });

export const Choice = Schema.Literals(["harness", "model", "effort"]);

export type ListedRole = typeof ListedRole.Type;
export type Role = typeof Role.Type;
export type Choice = typeof Choice.Type;

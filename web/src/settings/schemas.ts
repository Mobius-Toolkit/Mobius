import { Schema } from "effect";
import {
  GetVoiceLanguageResponse,
  ListDevicesResponse,
  ListGitHubAppsResponse,
  ListHarnessesResponse,
  ListMemoryFilesResponse,
  ListMemoryVersionsResponse,
  SetRoleParams,
} from "@/api/schemas.gen";

export const GitHubApp = ListGitHubAppsResponse.fields.data.value;
export const Devices = ListDevicesResponse.fields.data;
export const VoiceLanguage = GetVoiceLanguageResponse.fields.data;
export const MemoryFile = ListMemoryFilesResponse.fields.data.value;
export const MemoryVersion = ListMemoryVersionsResponse.fields.data.fields.versions.value;
export const HarnessChoices = ListHarnessesResponse.fields.data.value;
export const RoleName = SetRoleParams.fields.role;

export const SwitchKind = Schema.Literals(["start", "merge", "repairs"]);
export const SwitchStates = Schema.Struct({
  start: Schema.Boolean,
  merge: Schema.Boolean,
  repairs: Schema.Boolean,
});
export const SwitchErrors = Schema.Struct({
  start: Schema.String,
  merge: Schema.String,
  repairs: Schema.String,
});
export const SwitchChange = Schema.Struct({ kind: SwitchKind, on: Schema.Boolean });

export type GitHubApp = typeof GitHubApp.Type;
export type Devices = typeof Devices.Type;
export type VoiceLanguage = typeof VoiceLanguage.Type;
export type MemoryFile = typeof MemoryFile.Type;
export type MemoryVersion = typeof MemoryVersion.Type;
export type HarnessChoices = typeof HarnessChoices.Type;
export type RoleName = typeof RoleName.Type;
export type SwitchKind = typeof SwitchKind.Type;
export type SwitchStates = typeof SwitchStates.Type;
export type SwitchErrors = typeof SwitchErrors.Type;
export type SwitchChange = typeof SwitchChange.Type;

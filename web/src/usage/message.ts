import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import { Group, MenuKey, Period, QualityValue, Range, Search, Tab } from "./model";
import { Quality, SkillUsage, Usage, UsageTimes } from "./schemas";

export const Message = defineMessageUnion({
  LoadedUsage: { request: Schema.Number, usage: Usage, range: Range },
  LoadedTimes: { request: Schema.Number, times: UsageTimes, range: Range },
  LoadedSkills: { request: Schema.Number, skills: SkillUsage },
  LoadedQuality: { request: Schema.Number, quality: Quality, range: Range },
  LoadFailed: { request: Schema.Number, error: Schema.String },
  LoadLoginRequired: { request: Schema.Number },
  ReceivedSearch: { search: Search },
  SelectedTab: { tab: Tab },
  SelectedPeriod: { period: Period },
  ToggledFilter: { key: Group, option: Schema.String },
  ToggledGroup: { key: Group },
  ChangedFrom: { value: Schema.String },
  ChangedTo: { value: Schema.String },
  SelectedStat: { stat: Schema.String },
  SelectedValue: { value: QualityValue },
  ToggledMenu: { menu: MenuKey },
  ClosedMenu: {},
  OpenedGroupSheet: {},
  ClosedGroupSheet: {},
  PressedEscape: {},
  ResizedChart: { width: Schema.Number, height: Schema.Number },
  HoveredChart: { x: Schema.Number, y: Schema.Number },
  LeftChart: {},
  CompletedRequestLogin: {},
  CompletedChangeSearch: {},
  CompletedFocusSheet: {},
});

export type Message = typeof Message.Type;

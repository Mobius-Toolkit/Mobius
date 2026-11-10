import {
  GetUsageQualityResponse,
  GetUsageResponse,
  GetUsageSkillsResponse,
  GetUsageTimesResponse,
} from "@/api/schemas.gen";

export const Usage = GetUsageResponse.fields.data;
export const UsageTimes = GetUsageTimesResponse.fields.data;
export const SkillUsage = GetUsageSkillsResponse.fields.data;
export const Quality = GetUsageQualityResponse.fields.data;
export const UsageValues = Usage.fields.totals;
export const QualityValues = Quality.fields.totals;
export const TimeStat = UsageTimes.fields.totals.value;
export const SkillUsageRow = SkillUsage.fields.rows.value;

export type Usage = typeof Usage.Type;
export type UsageTimes = typeof UsageTimes.Type;
export type SkillUsage = typeof SkillUsage.Type;
export type Quality = typeof Quality.Type;
export type UsageValues = typeof UsageValues.Type;
export type QualityValues = typeof QualityValues.Type;
export type TimeStat = typeof TimeStat.Type;
export type SkillUsageRow = typeof SkillUsageRow.Type;

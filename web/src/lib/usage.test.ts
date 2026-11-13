import { describe, expect, it } from "vitest";
import { validateUsageSearch } from "./usage";

describe("the search of the Usage page", () => {
  it("gives no field for a missing search", () => {
    expect(validateUsageSearch({})).toEqual({});
  });

  it("keeps each valid value", () => {
    const search = {
      tab: "quality",
      period: "custom",
      from: "2026-11-01",
      to: "2026-11-13",
      group: ["model", "role"],
      stat: "step:review",
      value: "fixRounds",
      harness: ["claude"],
      model: ["opus", "sonnet"],
      modelVersion: ["5"],
      effort: ["high"],
      role: ["lead"],
      repository: ["owner/shop"],
      source: ["mobius"],
    };
    expect(validateUsageSearch(search)).toEqual(search);
  });

  it("keeps a period that needs no day", () => {
    expect(validateUsageSearch({ period: "7d" }).period).toBe("7d");
    expect(validateUsageSearch({ period: "30d" }).period).toBe("30d");
    expect(validateUsageSearch({ period: "month" }).period).toBe("month");
  });

  it("drops a value that is not valid and keeps the other fields", () => {
    expect(
      validateUsageSearch({
        tab: "cost",
        period: "year",
        from: "2026-13-01",
        to: "yesterday",
        group: "model",
        stat: "other:x",
        value: "bugs",
        harness: "claude",
        model: 5,
        role: null,
        repository: ["owner/shop"],
      }),
    ).toEqual({ repository: ["owner/shop"] });
  });

  it("drops the custom period when it lacks a valid day", () => {
    expect(validateUsageSearch({ period: "custom" }).period).toBeUndefined();
    expect(validateUsageSearch({ period: "custom", from: "2026-11-01" }).period).toBeUndefined();
    expect(validateUsageSearch({ period: "custom", from: "2026-11-01", to: "tomorrow" })).toEqual({
      from: "2026-11-01",
    });
  });

  it("checks the day format", () => {
    expect(validateUsageSearch({ from: "2026-1-1" }).from).toBeUndefined();
    expect(validateUsageSearch({ from: "2026-11-01T00:00" }).from).toBeUndefined();
    expect(validateUsageSearch({ from: 20261101 }).from).toBeUndefined();
  });

  it("checks the stat prefix and the text after it", () => {
    expect(validateUsageSearch({ stat: "state:merged" }).stat).toBe("state:merged");
    expect(validateUsageSearch({ stat: "step:" }).stat).toBeUndefined();
    expect(validateUsageSearch({ stat: "step" }).stat).toBeUndefined();
    expect(validateUsageSearch({ stat: 3 }).stat).toBeUndefined();
  });

  it("keeps the strings of a list and drops the other items", () => {
    expect(validateUsageSearch({ model: ["opus", 5, null, "sonnet"] }).model).toEqual([
      "opus",
      "sonnet",
    ]);
    expect(validateUsageSearch({ model: [] }).model).toBeUndefined();
    expect(validateUsageSearch({ model: [5] }).model).toBeUndefined();
  });

  it("keeps each group once and drops a name that is not a group", () => {
    expect(
      validateUsageSearch({ group: ["model", "bogus", 5, "model", "source", "modelVersion"] })
        .group,
    ).toEqual(["model", "source", "modelVersion"]);
    expect(validateUsageSearch({ group: ["bogus"] }).group).toBeUndefined();
    expect(validateUsageSearch({ group: [] }).group).toBeUndefined();
  });
});

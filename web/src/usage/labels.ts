import type { Group } from "./model";

export const sourceTitle = (source: string) => (source === "mobius" ? "Mobius" : "Harness");

function fieldTitle(field: Group, value: string) {
  if (value === "") {
    return "—";
  }
  return field === "source" ? sourceTitle(value) : value;
}

// A row has one value for each selected field, in the order of the fields of the tab.
export function groupTitle(group: readonly string[], fields: readonly Group[]) {
  if (fields.length === 0) {
    return "All";
  }
  return group.map((value, index) => fieldTitle(fields[index], value)).join(" · ");
}

export function groupHeader(
  fields: readonly Group[],
  groups: readonly { key: Group; title: string }[],
) {
  if (fields.length === 0) {
    return "Group";
  }
  return fields.map((key) => groups.find((item) => item.key === key)?.title ?? key).join(" · ");
}

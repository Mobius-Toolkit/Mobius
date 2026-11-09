export const keepEffort = (effort: string, efforts: readonly string[]) =>
  efforts.includes(effort) ? effort : (efforts[0] ?? "");

export const bindingText = (setting: { harness: string; model: string; effort: string }) =>
  [setting.harness, setting.model, setting.effort].filter(Boolean).join(" · ");

export type Reply =
  | { status: 204 }
  | { status: 400 | 401 | 404 | 409 | 422 | 500; data: { error: string } };

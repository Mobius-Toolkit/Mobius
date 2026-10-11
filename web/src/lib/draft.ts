export function chatDraftKey(organization: string, repository: string, workstream: number) {
  return `chatDraft:${organization}/${repository}/${workstream}`;
}

export function loadDraft(key: string) {
  try {
    return localStorage.getItem(key) ?? "";
  } catch {
    return "";
  }
}

export function saveDraft(key: string, text: string) {
  try {
    if (text) {
      localStorage.setItem(key, text);
    } else {
      localStorage.removeItem(key);
    }
  } catch {
    // The input works with no kept text.
  }
}

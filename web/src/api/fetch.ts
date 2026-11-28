const noBody = [204, 205, 304];

const readData = async (res: Response) => {
  const text = noBody.includes(res.status) ? "" : await res.text();
  if (res.headers.get("Content-Type")?.includes("json")) {
    return text ? JSON.parse(text) : {};
  }
  if (!res.ok) {
    return { error: `The server is not available (${res.status}).` };
  }
  if (text) {
    throw new Error("The answer is not JSON.");
  }
  return {};
};

export const customFetch = async <T>(url: string, options: RequestInit): Promise<T> => {
  const res = await fetch(url, options);
  const data = await readData(res);
  return { data, status: res.status, headers: res.headers } as T;
};

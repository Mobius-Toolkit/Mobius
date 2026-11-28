// Gives the text parts of a multipart body as an object from the name of a part to its text.
export function formFields(body: string) {
  return Object.fromEntries(
    [...body.matchAll(/name="([^"]+)"\r\n\r\n([\s\S]*?)\r\n--/g)].map((part) => [part[1], part[2]]),
  );
}

export function atEnd(list: HTMLElement) {
  return list.scrollHeight - list.scrollTop - list.clientHeight < 40;
}

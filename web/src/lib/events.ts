type ServerEvent = { event: string; data: unknown }

export function onEvent<E extends ServerEvent, K extends E['event']>(
  source: EventSource,
  event: K,
  listener: (data: Extract<E, { event: K }>['data']) => void,
) {
  const handle = (e: MessageEvent) => listener(JSON.parse(e.data))
  source.addEventListener(event, handle)
  return () => source.removeEventListener(event, handle)
}

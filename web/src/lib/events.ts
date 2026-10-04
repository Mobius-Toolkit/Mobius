type ServerEvent = { event: string; data: unknown }

export function onEvent<E extends ServerEvent, K extends E['event']>(
  source: EventSource,
  event: K,
  listener: (data: Extract<E, { event: K }>['data']) => void,
) {
  source.addEventListener(event, (e) => listener(JSON.parse(e.data)))
}

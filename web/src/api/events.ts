import type { components } from './schema'

type Schemas = components['schemas']

type LiveEventName = Schemas['LiveEvents']['event']

// openapi-typescript does not read contentSchema, so it gives `data: unknown`
// in LiveEvents. This map gives the data type of each event name.
type LiveEventData = {
  activity: Schemas['Activity']
}

export function onLiveEvent<E extends LiveEventName>(
  source: EventSource,
  event: E,
  listener: (data: LiveEventData[E]) => void,
) {
  source.addEventListener(event, (e) => {
    listener(JSON.parse(e.data) as LiveEventData[E])
  })
}

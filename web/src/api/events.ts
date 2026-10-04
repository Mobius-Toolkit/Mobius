import type { Activity, LiveEvents } from './api.gen'

type LiveEventName = LiveEvents['event']

type LiveEventData = {
  activity: Activity
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

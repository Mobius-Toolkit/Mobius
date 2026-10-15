import { useEffect, useState } from "react";
import { listCIStops, type LiveEvents, type CIStop } from "@/api/api.gen";
import { onEvent } from "./events";

export function useCIStops(source?: EventSource) {
  const [ciStops, setCIStops] = useState<CIStop[]>([]);

  useEffect(() => {
    if (!source) {
      return;
    }
    // A ciStops event that comes while the connection is down is lost, so each connection reads the stops.
    const load = () => {
      listCIStops()
        .then((res) => {
          if (res.status === 200) {
            setCIStops(res.data.data);
          }
        })
        .catch(() => {});
    };
    load();
    source.addEventListener("open", load);
    const remove = onEvent<LiveEvents, "ciStops">(source, "ciStops", setCIStops);
    return () => {
      source.removeEventListener("open", load);
      remove();
    };
  }, [source]);

  return ciStops;
}

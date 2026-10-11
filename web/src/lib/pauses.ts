import { useEffect, useState } from "react";
import { listPauses, type LiveEvents, type Pause } from "@/api/api.gen";
import { onEvent } from "./events";

export function usePauses(source?: EventSource) {
  const [pauses, setPauses] = useState<Pause[]>([]);

  useEffect(() => {
    if (!source) {
      return;
    }
    // A pauses event that comes while the connection is down is lost, so each connection reads the pauses.
    const load = () => {
      listPauses()
        .then((res) => {
          if (res.status === 200) {
            setPauses(res.data.data);
          }
        })
        .catch(() => {});
    };
    load();
    source.addEventListener("open", load);
    const remove = onEvent<LiveEvents, "pauses">(source, "pauses", setPauses);
    return () => {
      source.removeEventListener("open", load);
      remove();
    };
  }, [source]);

  return pauses;
}

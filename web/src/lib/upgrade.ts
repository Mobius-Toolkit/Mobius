import { useEffect, useState } from "react";
import {
  cancelDrain,
  getDrain,
  getRelease,
  getUpgrade,
  startUpgrade,
  type Drain,
  type LiveEvents,
} from "@/api/api.gen";
import { reloadOnNewBuild } from "./build";
import { onEvent } from "./events";

export type Upgrade = ReturnType<typeof useUpgrade>;

export function useUpgrade(source?: EventSource) {
  const [version, setVersion] = useState("");
  const [drain, setDrain] = useState<Drain>();
  const [failure, setFailure] = useState("");
  const [upgrading, setUpgrading] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const [changesShown, setChangesShown] = useState(false);

  useEffect(() => {
    // A failed check keeps the version of the last good check.
    const check = () => {
      getRelease()
        .then((res) => {
          if (res.status === 200) {
            setVersion(res.data.data.version);
          }
        })
        .catch(() => {});
    };
    check();
    const timer = setInterval(check, 60 * 60 * 1000);
    return () => clearInterval(timer);
  }, []);

  useEffect(() => {
    if (!source) {
      return;
    }
    // The server sends a drain event only at a change, so each connection reads the state.
    const load = () => {
      getDrain()
        .then((res) => {
          if (res.status === 200) {
            setDrain(res.data.data);
          }
        })
        .catch(() => {});
      getUpgrade()
        .then((res) => {
          if (res.status === 200) {
            setFailure(res.data.data.failure);
          }
        })
        .catch(() => {});
    };
    load();
    source.addEventListener("open", load);
    const removeDrain = onEvent<LiveEvents, "drain">(source, "drain", setDrain);
    const removeUpgrade = onEvent<LiveEvents, "upgrade">(source, "upgrade", (upgrade) =>
      setFailure(upgrade.failure),
    );
    return () => {
      source.removeEventListener("open", load);
      removeDrain();
      removeUpgrade();
    };
  }, [source]);

  const start = () => {
    setChangesShown(false);
    setUpgrading(true);
    setFailure("");
    startUpgrade()
      .then((res) => {
        if (res.status === 200 && res.data.data.end === "drained") {
          void reloadOnNewBuild();
          return;
        }
        if (res.status !== 200) {
          setFailure(res.data.error);
        }
        setUpgrading(false);
      })
      .catch((err: unknown) => {
        setFailure(String(err));
        setUpgrading(false);
      });
  };

  const cancel = () => {
    setCancelling(true);
    cancelDrain()
      .then((res) => {
        if (res.status !== 204) {
          setFailure(res.data.error);
        }
      })
      .catch((err: unknown) => setFailure(String(err)))
      .finally(() => setCancelling(false));
  };

  return {
    version,
    drain,
    failure,
    upgrading,
    cancelling,
    changesShown,
    setChangesShown,
    start,
    cancel,
  };
}

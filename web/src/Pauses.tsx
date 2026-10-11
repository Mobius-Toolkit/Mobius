import { use, useState } from "react";
import { resume, type Pause } from "@/api/api.gen";
import { ErrorBadge } from "@/components/page";
import { Button } from "@/components/ui/button";
import { LoginContext } from "@/lib/login";
import { dayClock } from "@/lib/time";

export function Pauses({ pauses }: { pauses: Pause[] }) {
  return pauses.map((pause) => <PauseLine key={pause.harness} pause={pause} />);
}

function PauseLine({ pause }: { pause: Pause }) {
  const showLogin = use(LoginContext);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState("");

  const resumeNow = () => {
    setRunning(true);
    resume(pause.harness)
      .then((res) => {
        if (res.status === 204) {
          setError("");
          return;
        }
        if (res.status === 401) {
          showLogin();
        } else {
          setError(res.data.error);
        }
        setRunning(false);
      })
      .catch((err: unknown) => {
        setError(String(err));
        setRunning(false);
      });
  };

  return (
    <div className="grid gap-2">
      <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-amber-500/40 bg-amber-100 px-3 py-2 text-sm text-amber-900 dark:bg-amber-950 dark:text-amber-200">
        <span>
          {pause.harness} paused until {dayClock(pause.pausedUntil)}
        </span>
        <Button size="sm" disabled={running} pending={running} onClick={resumeNow}>
          Resume now
        </Button>
      </div>
      {error && <ErrorBadge>{error}</ErrorBadge>}
    </div>
  );
}

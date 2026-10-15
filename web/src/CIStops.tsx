import type { CIStop } from "@/api/api.gen";

export function CIStops({ stops }: { stops: CIStop[] }) {
  return stops.map((stop) => (
    <div
      key={`${stop.repository} ${stop.issue}`}
      className="grid gap-1 rounded-lg border border-destructive/40 bg-red-100 px-3 py-2 text-sm wrap-anywhere text-red-900 dark:bg-red-950 dark:text-red-200"
    >
      <span className="font-medium">Dispatch stopped: CI fails on {stop.branch}</span>
      <span>
        {stop.repository}: the workflow "{stop.workflow}" fails.
      </span>
      <span>
        Task issue:{" "}
        <a href={stop.issueUrl} target="_blank" rel="noreferrer" className="underline">
          #{stop.issue}
        </a>
      </span>
    </div>
  ));
}

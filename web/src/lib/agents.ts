export function queueState(queueReason: string) {
  if (queueReason === "runs .mobius/check") {
    return { badge: "checks", dot: "bg-green-600" };
  }
  if (queueReason === "waits for a check slot") {
    return { badge: "waits for check", dot: "bg-amber-500" };
  }
  if (queueReason.startsWith("paused until ")) {
    return { badge: "paused", dot: "bg-amber-500" };
  }
  if (queueReason) {
    return { badge: "queued", dot: "bg-amber-500" };
  }
  return { badge: "", dot: "bg-green-600" };
}

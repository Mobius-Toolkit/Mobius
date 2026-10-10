export function clock(time: string) {
  return new Date(time).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function dayClock(time: string) {
  return new Date(time).toLocaleString([], {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function dayLabel(time: string) {
  const date = new Date(time);
  const now = new Date();
  if (date.toDateString() === now.toDateString()) {
    return "Today";
  }
  if (
    date.toDateString() ===
    new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1).toDateString()
  ) {
    return "Yesterday";
  }
  return date.toLocaleDateString([], {
    weekday: "short",
    month: "short",
    day: "numeric",
    year: date.getFullYear() === now.getFullYear() ? undefined : "numeric",
  });
}

const utcTime = /(\d{4}-\d{2}-\d{2}) (\d{2}:\d{2}) UTC/g;

export function localTimes(text: string) {
  return text.replace(utcTime, (_, date: string, time: string) => dayClock(`${date}T${time}:00Z`));
}

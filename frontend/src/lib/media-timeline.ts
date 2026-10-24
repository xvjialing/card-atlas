import type { Media } from "./devices";

// 相机日期不含时区，直接校验并比较原始日历值，避免跨时区移动日期。
export function captureDate(value: string): string | null {
  const match = /^(\d{4})-(\d{2})-(\d{2}) (\d{2}):(\d{2}):(\d{2})$/.exec(value);
  if (!match) return null;
  const [, year, month, day, hour, minute, second] = match.map(Number);
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  if (
    year < 1 ||
    month < 1 ||
    month > 12 ||
    day < 1 ||
    day > days[month - 1] ||
    hour > 23 ||
    minute > 59 ||
    second > 59
  )
    return null;
  return value;
}

export function sortByCaptureDate<T extends Pick<Media, "date">>(
  media: readonly T[],
): T[] {
  return media
    .map((item) => ({ item, date: captureDate(item.date) ?? "" }))
    .sort((a, b) => (a.date < b.date ? 1 : a.date > b.date ? -1 : 0))
    .map(({ item }) => item);
}

export function groupByCaptureDay<T extends Pick<Media, "date">>(
  media: readonly T[],
) {
  const groups = new Map<string, { day: string; label: string; media: T[] }>();
  for (const item of media) {
    const day = captureDate(item.date)?.slice(0, 10) ?? "unknown";
    let group = groups.get(day);
    if (!group) {
      const [year, month, date] = day.split("-");
      group = {
        day,
        label:
          day === "unknown"
            ? "拍摄时间未知"
            : `${year}年${Number(month)}月${Number(date)}日`,
        media: [],
      };
      groups.set(day, group);
    }
    group.media.push(item);
  }
  return [...groups.values()];
}

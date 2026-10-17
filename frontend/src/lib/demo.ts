import type { Filter, MediaFile, Summary, Plan } from "./types";

// 浏览器预览使用明确标注的演示数据；实际文件操作只通过 Go 服务执行。
export const demoFiles: MediaFile[] = Array.from({ length: 248 }, (_, i) => {
  const kind = i % 7 === 0 ? "video" : "photo";
  const day = 8 + Math.floor(i / 36);
  const ext = kind === "video" ? "MP4" : i % 3 === 0 ? "ARW" : "JPG";
  const name = `${kind === "video" ? "C" : "DSC"}${String(1041 + i).padStart(5, "0")}.${ext}`;
  return {
    id: `demo-${i}`,
    name,
    relativePath: `DCIM/100MSDCF/${name}`,
    kind,
    extension: ext,
    size:
      kind === "video"
        ? (350 + i * 3) * 1024 ** 2
        : (14 + (i % 28)) * 1024 ** 2,
    date: `2026-10-${String(day).padStart(2, "0")}T${String(8 + (i % 10)).padStart(2, "0")}:24:12`,
    dateSource:
      kind === "video"
        ? "视频创建时间"
        : i % 9 === 0
          ? "文件修改时间"
          : "EXIF 拍摄时间",
    modified: 0,
  };
}).sort((a, b) => b.date.localeCompare(a.date));
export function demoSummary(): Summary {
  const days = [...new Set(demoFiles.map((f) => f.date.slice(0, 10)))]
    .sort()
    .map((date) => {
      const files = demoFiles.filter((f) => f.date.startsWith(date));
      return {
        date,
        photos: files.filter((f) => f.kind === "photo").length,
        videos: files.filter((f) => f.kind === "video").length,
        bytes: files.reduce((n, f) => n + f.size, 0),
      };
    });
  return {
    path: "/Volumes/SONY_ALPHA/DCIM",
    count: demoFiles.length,
    photos: demoFiles.filter((f) => f.kind === "photo").length,
    videos: demoFiles.filter((f) => f.kind === "video").length,
    bytes: demoFiles.reduce((n, f) => n + f.size, 0),
    fallback: demoFiles.filter((f) => f.dateSource === "文件修改时间").length,
    ignored: 12,
    warnings: [],
    days,
  };
}
export function filteredDemo(q: Filter) {
  return demoFiles
    .filter(
      (f) =>
        (q.kind === "all" || f.kind === q.kind) &&
        (!q.from || f.date.slice(0, 10) >= q.from) &&
        (!q.to || f.date.slice(0, 10) <= q.to) &&
        f.relativePath.toLowerCase().includes(q.search.toLowerCase()),
    )
    .sort((a, b) =>
      q.sort === "size"
        ? b.size - a.size
        : q.sort === "name"
          ? a.name.localeCompare(b.name)
          : b.date.localeCompare(a.date),
    );
}
export function demoPlan(ids: string[]): Plan {
  const files = demoFiles.filter((f) => ids.includes(f.id));
  const path = (f: MediaFile) =>
    `${f.kind}/${f.date.slice(0, 10).replaceAll("-", "/")}`;
  return {
    count: files.length,
    bytes: files.reduce((n, f) => n + f.size, 0),
    directories: [...new Set(files.map(path))].sort().map((date) => ({
      date,
      photos: files.filter((f) => path(f) === date && f.kind === "photo")
        .length,
      videos: files.filter((f) => path(f) === date && f.kind === "video")
        .length,
      bytes: files
        .filter((f) => path(f) === date)
        .reduce((n, f) => n + f.size, 0),
    })),
    examples: files.slice(0, 6).map((f) => `${path(f)}/${f.name}`),
  };
}

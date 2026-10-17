export interface MediaFile {
  id: string;
  name: string;
  relativePath: string;
  kind: string;
  extension: string;
  size: number;
  date: string;
  dateSource: string;
  modified: number;
}
export interface DayStat {
  date: string;
  photos: number;
  videos: number;
  bytes: number;
}
export interface Summary {
  path: string;
  count: number;
  photos: number;
  videos: number;
  bytes: number;
  fallback: number;
  ignored: number;
  warnings: string[];
  days: DayStat[];
}
export interface Job {
  id: number;
  kind: string;
  state: string;
  current: string;
  completed: number;
  total: number;
  bytes: number;
  totalBytes: number;
  copied: number;
  failed: number;
  errors: string[];
  destination: string;
  phase: string;
  currentBytes: number;
  currentSize: number;
  workDone: number;
  totalWork: number;
  startedAt: number;
  finishedAt: number;
}
export interface Thumbnail {
  dataURL: string;
  message: string;
}
export interface Filter {
  search: string;
  kind: string;
  from: string;
  to: string;
  page: number;
  sort: string;
}
export interface FilePage {
  files: MediaFile[];
  total: number;
  bytes: number;
}
export interface Plan {
  count: number;
  bytes: number;
  directories: DayStat[];
  examples: string[];
}
export const emptySummary: Summary = {
  path: "",
  count: 0,
  photos: 0,
  videos: 0,
  bytes: 0,
  fallback: 0,
  ignored: 0,
  warnings: [],
  days: [],
};
export const idleJob: Job = {
  id: 0,
  kind: "",
  state: "idle",
  current: "",
  completed: 0,
  total: 0,
  bytes: 0,
  totalBytes: 0,
  copied: 0,
  failed: 0,
  errors: [],
  destination: "",
  phase: "",
  currentBytes: 0,
  currentSize: 0,
  workDone: 0,
  totalWork: 0,
  startedAt: 0,
  finishedAt: 0,
};
export function bytes(value: number) {
  if (!value) return "0 B";
  const i = Math.min(4, Math.floor(Math.log(value) / Math.log(1024)));
  return `${(value / 1024 ** i).toLocaleString("zh-CN", { maximumFractionDigits: i ? 2 : 0 })} ${["B", "KB", "MB", "GB", "TB"][i]}`;
}
export const number = (n: number) => n.toLocaleString("zh-CN");

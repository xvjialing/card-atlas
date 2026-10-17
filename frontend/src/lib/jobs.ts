import type { Job } from "./types";

export function exportProgress(job: Job) {
  if (job.state === "completed") return 100;
  const progress = job.totalWork
    ? (job.workDone / job.totalWork) * 100
    : job.total
      ? (job.completed / job.total) * 100
      : 0;
  return Math.min(job.state === "running" ? 99.9 : 100, Math.max(0, progress));
}
export function exportTitle(job: Job) {
  if (job.state === "starting" || job.phase === "preparing")
    return "正在准备复制";
  if (job.state === "running")
    return job.phase === "verifying" ? "正在校验文件" : "正在复制文件";
  if (job.state === "cancelled") return "复制已取消";
  if (job.state === "failed" || (job.failed > 0 && job.copied === 0))
    return "导出失败";
  if (job.failed > 0) return "导出部分完成";
  return "导出成功";
}

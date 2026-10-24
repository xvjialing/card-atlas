import type { Task } from "./devices";

// 按后端规划的任务身份取本次范围，避免把以前的导入算入本次进度。
export function scopedTasks(tasks: readonly Task[], ids: readonly string[]) {
  const scope = new Set(ids);
  return tasks.filter((task) => scope.has(task.id));
}
export function importSummary(tasks: readonly Task[]) {
  const completed = tasks.filter((task) => task.status === "completed").length;
  const failed = tasks.filter((task) => task.status === "failed").length;
  const paused = tasks.filter((task) => task.status === "paused").length;
  const pending = tasks.filter(
    (task) => task.status === "downloading" || task.status === "queued",
  ).length;
  const totalBytes = tasks.reduce(
    (sum, task) => sum + Math.max(task.progress.total, task.media.size),
    0,
  );
  const transferred = tasks.reduce(
    (sum, task) =>
      sum +
      Math.min(
        Math.max(task.progress.bytes, 0),
        Math.max(task.progress.total, task.media.size),
      ),
    0,
  );
  const percent =
    tasks.length > 0 && completed === tasks.length
      ? 100
      : totalBytes
        ? Math.min(99.9, (100 * transferred) / totalBytes)
        : null;
  const title = pending
    ? "正在导入原始素材"
    : paused
      ? "导入已暂停"
      : failed
        ? completed
          ? "导入部分完成"
          : "导入失败"
        : tasks.length
          ? "导入完成"
          : "导入任务与记录";
  return {
    completed,
    failed,
    paused,
    pending,
    totalBytes,
    transferred,
    percent,
    title,
  };
}

import { useEffect, useState } from "react";
import {
  CheckCheck,
  FolderOpen,
  LoaderCircle,
  ShieldCheck,
  TriangleAlert,
  X,
} from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import { bytes, number, type Job } from "@/lib/types";
import { exportProgress, exportTitle } from "@/lib/jobs";

export function ExportTaskDialog({
  job,
  open,
  onOpenChange,
  onCancel,
  onHistory,
  monitorError,
  onRetry,
}: {
  job: Job;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCancel: () => Promise<void>;
  onHistory: () => void;
  monitorError: string;
  onRetry: () => void;
}) {
  const [cancelling, setCancelling] = useState(false);
  const [cancelError, setCancelError] = useState("");
  useEffect(() => {
    setCancelling(false);
    setCancelError("");
  }, [job.id]);
  const running = job.state === "running" || job.state === "starting";
  const problem = job.failed > 0 || job.state === "failed";
  const percent = exportProgress(job);
  const title = exportTitle(job);
  const elapsed = Math.max(
    0,
    ((job.finishedAt || Date.now()) - job.startedAt) / 1000,
  );
  const description = running
    ? "文件正在复制到目标目录，完成后逐个校验内容。可以取消任务，或关闭窗口让任务继续。"
    : job.state === "cancelled"
      ? "已完成的文件已保留，当前不完整文件已清理。"
      : problem
        ? job.copied > 0
          ? "部分素材未能完成复制。请查看下方原因，已成功复制的文件会保留。"
          : "文件未完成复制。请查看下方原因，检查后重新选择文件重试。"
        : "所有选中文件已复制并通过 SHA-256 校验，原始素材保留不变。";
  const cancel = async () => {
    setCancelling(true);
    setCancelError("");
    try {
      await onCancel();
    } catch (e) {
      setCancelError(e instanceof Error ? e.message : String(e));
      setCancelling(false);
    }
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="export-task-dialog">
        <DialogHeader>
          <div
            className={`task-title-icon ${problem ? "error" : running ? "pending" : job.state === "cancelled" ? "cancelled" : "success"}`}
          >
            {running ? (
              <LoaderCircle className="spin" size={23} />
            ) : problem ? (
              <TriangleAlert size={23} />
            ) : job.state === "cancelled" ? (
              <X size={23} />
            ) : (
              <CheckCheck size={23} />
            )}
          </div>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <div className="task-destination">
          <FolderOpen size={17} />
          <span>{job.destination}</span>
        </div>
        {monitorError && (
          <div className="task-errors" role="alert">
            <p>{monitorError}</p>
            <Button variant="outline" size="sm" onClick={onRetry}>
              重新获取进度
            </Button>
          </div>
        )}
        <div className="task-overall">
          <div>
            <span>
              {running
                ? "总进度（复制 + 校验）"
                : job.state === "cancelled"
                  ? "取消时进度"
                  : "已处理进度"}
            </span>
            <strong>{percent.toFixed(1)}%</strong>
          </div>
          <Progress value={percent} aria-label="复制与校验总进度" />
          <p>
            {number(job.completed)} / {number(job.total)} 个文件已处理
            {job.startedAt > 0 && (
              <span>
                用时{" "}
                {elapsed < 60
                  ? `${Math.floor(elapsed)} 秒`
                  : `${Math.floor(elapsed / 60)} 分 ${Math.floor(elapsed % 60)} 秒`}
              </span>
            )}
          </p>
        </div>
        {running && (
          <div className="task-current">
            <div>
              <span>
                {job.phase === "verifying" ? "校验当前文件" : "复制当前文件"}
              </span>
              <span>
                {bytes(job.currentBytes)} / {bytes(job.currentSize)}
              </span>
            </div>
            <strong title={job.current}>
              {job.current || "正在准备文件…"}
            </strong>
            {job.currentSize > 0 && (
              <Progress
                value={(job.currentBytes / job.currentSize) * 100}
                aria-label="当前文件进度"
              />
            )}
            {job.phase === "verifying" && (
              <p>
                <ShieldCheck size={13} />
                正在读回目标文件，验证 SHA-256
              </p>
            )}
          </div>
        )}
        <div className="task-counts" role="status">
          <div>
            <span>复制成功</span>
            <strong>{number(job.copied)}</strong>
          </div>
          <div className={job.failed ? "has-errors" : ""}>
            <span>复制失败</span>
            <strong>{number(job.failed)}</strong>
          </div>
          <div>
            <span>{running ? "待处理" : "未完成"}</span>
            <strong>{number(Math.max(0, job.total - job.completed))}</strong>
          </div>
        </div>
        {!running && (
          <p className="task-result-bytes">
            已保留 {bytes(job.bytes)} 的复制文件
          </p>
        )}
        {(job.errors.length > 0 || cancelError) && (
          <div className="task-errors" role="alert">
            <strong>失败原因</strong>
            {cancelError && <p>{cancelError}</p>}
            {job.errors.map((message, index) => (
              <p key={index}>{message}</p>
            ))}
            <p className="task-recovery">
              请检查存储设备连接、目录权限及剩余空间，再选择未完成的文件重试。
            </p>
          </div>
        )}
        <DialogFooter>
          {running ? (
            <>
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                后台继续
              </Button>
              <Button
                variant="outline"
                onClick={cancel}
                disabled={job.state === "starting" || cancelling}
              >
                {cancelling ? (
                  <LoaderCircle className="spin" size={15} />
                ) : (
                  <X size={15} />
                )}{" "}
                {cancelling ? "正在取消" : "取消复制"}
              </Button>
            </>
          ) : (
            <>
              <Button variant="outline" onClick={onHistory}>
                查看导出记录
              </Button>
              <Button onClick={() => onOpenChange(false)}>完成</Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

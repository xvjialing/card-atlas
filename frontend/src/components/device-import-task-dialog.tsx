import { useEffect, useMemo, useState, type RefObject } from "react";
import {
  CheckCheck,
  ChevronLeft,
  ChevronRight,
  FolderOpen,
  LoaderCircle,
  Pause,
  RefreshCw,
  TriangleAlert,
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
import { taskLabel, type Session, type Task } from "@/lib/devices";
import { importSummary, scopedTasks } from "@/lib/device-tasks";
import { bytes, number } from "@/lib/types";

export type ImportTaskView = "current" | "history";
export function DeviceImportTaskDialog({
  open,
  onOpenChange,
  tasks,
  taskIDs,
  view,
  onViewChange,
  connection,
  providerID,
  busy,
  error,
  notice,
  monitorError,
  warning,
  onRefresh,
  onPause,
  onResume,
  onRetry,
  onReveal,
  onAnalyze,
  returnFocus,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  tasks: Task[];
  taskIDs: string[];
  view: ImportTaskView;
  onViewChange: (view: ImportTaskView) => void;
  connection: Session;
  providerID: string;
  busy: boolean;
  error: string;
  notice: string;
  monitorError: string;
  warning: string;
  onRefresh: () => void;
  onPause: () => void;
  onResume: () => void;
  onRetry: (id: string) => void;
  onReveal: (id: string) => void;
  onAnalyze: (path: string) => void;
  returnFocus: RefObject<HTMLButtonElement | null>;
}) {
  const [page, setPage] = useState(1);
  const [expanded, setExpanded] = useState("");
  const scope = useMemo(() => scopedTasks(tasks, taskIDs), [tasks, taskIDs]);
  const records = view === "current" ? scope : [...tasks].reverse();
  const summary = importSummary(records);
  const pending = tasks.some(
    (task) => task.status === "queued" || task.status === "downloading",
  );
  const awaiting =
    view === "current" && taskIDs.length > 0 && scope.length < taskIDs.length;
  const active = records.find((task) => task.status === "downloading");
  const pages = Math.max(1, Math.ceil(records.length / 10));
  const currentPage = Math.min(page, pages);
  const visible = records.slice((currentPage - 1) * 10, currentPage * 10);
  const scopeKey = taskIDs.join("|");
  useEffect(() => {
    setPage(1);
    setExpanded("");
  }, [view, scopeKey]);
  useEffect(() => {
    setPage((p) => Math.min(p, pages));
  }, [pages]);
  const canContinue = (task: Task) =>
    connection.phase === "connected" &&
    task.deviceId === connection.deviceId &&
    task.providerId === providerID;
  const resumable = tasks.some(
    (task) => ["paused", "failed"].includes(task.status) && canContinue(task),
  );
  const roots = [...new Set(records.map((task) => task.root))];
  const title =
    view === "history"
      ? "导入任务与记录"
      : awaiting
        ? "正在获取导入进度"
        : summary.title;
  const inProgress = awaiting || summary.pending > 0;
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="export-task-dialog device-import-task-dialog"
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          returnFocus.current?.focus();
        }}
      >
        <DialogHeader>
          <div
            className={`task-title-icon ${summary.failed ? "error" : inProgress ? "pending" : summary.paused ? "cancelled" : "success"}`}
          >
            {inProgress ? (
              <LoaderCircle className="spin" size={23} />
            ) : summary.failed ? (
              <TriangleAlert size={23} />
            ) : summary.paused ? (
              <Pause size={23} />
            ) : (
              <CheckCheck size={23} />
            )}
          </div>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>
            {inProgress
              ? "原始素材正在保存到本机。关闭弹窗会继续传输，暂停会保留续传片段。"
              : view === "history"
                ? "查看本机保存的导入记录、文件位置和未完成原因。"
                : summary.failed
                  ? "请查看未完成原因，已保存的文件会保留，可以重新连接原设备后重试。"
                  : summary.paused
                    ? "已完成文件和续传片段均已保留，连接原设备后可继续。"
                    : "设备原片保留不变。已检查下载长度并生成本地指纹，不代表源端哈希核验。"}
          </DialogDescription>
        </DialogHeader>
        <div
          className="device-segmented device-task-switch"
          role="group"
          aria-label="导入任务范围"
        >
          <Button
            size="sm"
            variant="ghost"
            disabled={!taskIDs.length}
            aria-pressed={view === "current"}
            onClick={() => onViewChange("current")}
          >
            本次导入{taskIDs.length ? ` · ${number(taskIDs.length)}` : ""}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            aria-pressed={view === "history"}
            onClick={() => onViewChange("history")}
          >
            全部记录 · {number(tasks.length)}
          </Button>
        </div>
        <div className="device-import-task-body">
          {monitorError && (
            <div className="task-errors" role="alert">
              <p>{monitorError}</p>
              <Button variant="outline" size="sm" onClick={onRefresh}>
                重新获取进度
              </Button>
            </div>
          )}
          {warning && (
            <div className="task-errors" role="alert">
              {warning}
            </div>
          )}
          {error && (
            <div className="task-errors" role="alert">
              {error}
            </div>
          )}
          {notice && (
            <p className="device-task-notice" role="status">
              {notice}
            </p>
          )}
          {view === "current" && roots.length > 0 && (
            <div className="task-destination">
              <FolderOpen size={17} />
              <span>
                {roots.length === 1
                  ? roots[0]
                  : `本次素材将保存到 ${roots.length} 个目标目录，可在详情中查看。`}
              </span>
            </div>
          )}
          {view === "current" && records.length > 0 && (
            <div className="task-overall">
              <div>
                <span>
                  {summary.completed === records.length
                    ? "本次导入完成"
                    : "传输进度（容量按设备清单估计）"}
                </span>
                <strong>
                  {summary.percent === null
                    ? "—"
                    : `${summary.percent.toFixed(1)}%`}
                </strong>
              </div>
              <Progress value={summary.percent} aria-label="本次设备导入进度" />
              <p>
                已保存 {number(summary.completed)} / {number(taskIDs.length)} 项
                <span>
                  {bytes(summary.transferred)} / {bytes(summary.totalBytes)}
                </span>
              </p>
            </div>
          )}
          {awaiting && (
            <p className="device-task-notice" role="status">
              <LoaderCircle className="spin" size={16} />
              正在读取本次任务，请稍候…
            </p>
          )}
          {active && (
            <div className="task-current">
              <div>
                <span>{taskLabel(active)}</span>
                <span>
                  {bytes(active.progress.bytes)} /{" "}
                  {bytes(active.progress.total)}
                </span>
              </div>
              <strong>{active.media.name}</strong>
              <Progress
                value={
                  active.progress.total
                    ? Math.min(
                        99.9,
                        (100 * active.progress.bytes) / active.progress.total,
                      )
                    : null
                }
                aria-label="当前素材导入进度"
              />
              <p>
                {active.progress.phase === "fingerprinting"
                  ? "已接收文件，正在读取本地文件生成 SHA-256 指纹。"
                  : active.progress.phase === "publishing"
                    ? "正在保存最终文件并检查写入结果。"
                    : active.progress.speed > 0
                      ? `当前速度 ${bytes(active.progress.speed)}/s`
                      : "正在等待设备响应。"}
              </p>
            </div>
          )}
          <div className="task-counts device-import-counts" role="status">
            <div>
              <span>已完成</span>
              <strong>{number(summary.completed)}</strong>
            </div>
            <div>
              <span>待完成</span>
              <strong>{number(summary.pending)}</strong>
            </div>
            <div>
              <span>已暂停</span>
              <strong>{number(summary.paused)}</strong>
            </div>
            <div className={summary.failed ? "has-errors" : ""}>
              <span>失败</span>
              <strong>{number(summary.failed)}</strong>
            </div>
          </div>
          {!records.length && !awaiting ? (
            <div className="device-empty-tasks">
              尚无导入记录。连接设备并选择素材后，任务会显示在这里。
            </div>
          ) : (
            <div className="device-task-records">
              {visible.map((task) => (
                <article className="device-task-record" key={task.id}>
                  <div className="device-task-record-top">
                    <div className="device-task-record-name">
                      <strong>{task.media.name}</strong>
                      <small>
                        {task.deviceName} · {task.media.storageName} ·{" "}
                        {bytes(
                          task.status === "completed"
                            ? task.progress.total
                            : task.media.size,
                        )}
                      </small>
                    </div>
                    <span className={`device-task-status ${task.status}`}>
                      <span className="status-dot" />
                      {taskLabel(task)}
                    </span>
                  </div>
                  <div className="device-task-actions">
                    {["failed", "paused"].includes(task.status) && (
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={busy || !canContinue(task)}
                        onClick={() => onRetry(task.id)}
                      >
                        继续导入
                      </Button>
                    )}
                    {task.status === "completed" && (
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={busy}
                        onClick={() => onReveal(task.id)}
                      >
                        <FolderOpen size={15} />
                        查看文件
                      </Button>
                    )}
                    <Button
                      size="sm"
                      variant="ghost"
                      aria-label={`查看 ${task.media.name} 导入详情`}
                      aria-expanded={expanded === task.id}
                      aria-controls={`device-task-detail-${task.id}`}
                      onClick={() =>
                        setExpanded((value) =>
                          value === task.id ? "" : task.id,
                        )
                      }
                    >
                      {expanded === task.id ? "收起详情" : "详情"}
                    </Button>
                  </div>
                  {task.error && (
                    <p className="device-task-error">{task.error}</p>
                  )}
                  {["failed", "paused"].includes(task.status) &&
                    !canContinue(task) && (
                      <p className="device-footnote">
                        请连接任务所属的原设备后继续。
                      </p>
                    )}
                  {expanded === task.id && (
                    <div
                      id={`device-task-detail-${task.id}`}
                      className="device-task-detail"
                    >
                      <dl className="device-detail-list">
                        <dt>设备原路径</dt>
                        <dd>{task.media.path}</dd>
                        <dt>保存位置</dt>
                        <dd>
                          {task.path || `${task.root}/${task.relativePath}`}
                        </dd>
                        <dt>拍摄时间</dt>
                        <dd>
                          {task.media.date || "未知"} · {task.media.dateSource}
                        </dd>
                        {task.sha256 && (
                          <>
                            <dt>本地 SHA-256</dt>
                            <dd>{task.sha256}</dd>
                          </>
                        )}
                      </dl>
                      {task.warning && (
                        <p className="device-footnote">{task.warning}</p>
                      )}
                      {task.status === "completed" && (
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => onAnalyze(task.root)}
                        >
                          在素材分析中打开此目录
                        </Button>
                      )}
                    </div>
                  )}
                </article>
              ))}
            </div>
          )}
          {records.length > 0 && (
            <div className="device-pagination">
              <span>
                第 {currentPage} / {pages} 页 · 每页 10 项
              </span>
              <Button
                variant="ghost"
                size="icon"
                aria-label="导入记录上一页"
                disabled={currentPage <= 1}
                onClick={() => setPage((n) => n - 1)}
              >
                <ChevronLeft size={17} />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                aria-label="导入记录下一页"
                disabled={currentPage >= pages}
                onClick={() => setPage((n) => n + 1)}
              >
                <ChevronRight size={17} />
              </Button>
            </div>
          )}
          <p className="device-footnote">
            导入记录保存在本机。预览不等于原片已保存；本地指纹不代表已取得设备端哈希。
          </p>
        </div>
        <DialogFooter>
          {pending ? (
            <Button variant="outline" disabled={busy} onClick={onPause}>
              <Pause size={15} />
              暂停全部
            </Button>
          ) : resumable ? (
            <Button
              variant="outline"
              disabled={busy || connection.busy}
              onClick={onResume}
            >
              <RefreshCw size={15} />
              继续未完成任务
            </Button>
          ) : null}
          <Button onClick={() => onOpenChange(false)}>
            {pending ? "后台继续" : view === "current" ? "完成" : "关闭"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

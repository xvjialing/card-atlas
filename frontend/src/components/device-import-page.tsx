import { useEffect, useMemo, useRef, useState } from "react";
import {
  ArrowDownToLine,
  ArrowRight,
  Bluetooth,
  Camera,
  CheckCheck,
  ChevronLeft,
  ChevronRight,
  FolderOpen,
  HardDrive,
  History,
  LoaderCircle,
  RefreshCw,
  Search,
  ShieldCheck,
  Unplug,
  Wifi,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";
import { Badge } from "@/components/ui/badge";
import {
  Select as DeviceSelect,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { DeviceMediaBrowser } from "./device-media-browser";
import {
  DeviceImportTaskDialog,
  type ImportTaskView,
} from "./device-import-task-dialog";
import { captureDate, sortByCaptureDate } from "@/lib/media-timeline";
import { mediaSelectionState, setMediaSelection } from "@/lib/media-selection";
import { DeviceConnectionDialog } from "./device-connection-dialog";
import {
  devicesAPI,
  emptyDeviceState,
  errorMessage,
  deviceMediaURL,
  type Media,
  type Plan,
} from "@/lib/devices";
import { bytes, number } from "@/lib/types";
import { isDesktop } from "@/lib/api";

const phaseLabel: Record<string, string> = {
  disconnected: "未连接",
  preparing: "等待配对",
  choosing_wifi: "选择热点",
  waiting_wifi: "等待连接热点",
  connecting: "正在连接",
  connection_error: "连接未完成",
  connected: "已连接",
  lost: "连接已中断",
};

export function DeviceImportPage({
  onLocal,
  onAnalyze,
  demo,
  onPending,
}: {
  onLocal: () => void;
  onAnalyze: (path: string) => void;
  demo: boolean;
  onPending: (count: number) => void;
}) {
  const [state, setState] = useState(emptyDeviceState);
  const [media, setMedia] = useState<Media[]>([]);
  const [selected, setSelected] = useState(new Set<string>());
  const [connectionOpen, setConnectionOpen] = useState(false);
  const [acting, setActing] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [monitorError, setMonitorError] = useState("");
  const [revision, setRevision] = useState(0);
  const [loading, setLoading] = useState(false);
  const [searchText, setSearchText] = useState("");
  const [search, setSearch] = useState("");
  const [composing, setComposing] = useState(false);
  const [kind, setKind] = useState("all");
  const [storage, setStorage] = useState("all");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [onlyNew, setOnlyNew] = useState(false);
  const [page, setPage] = useState(1);
  const [tasksOpen, setTasksOpen] = useState(false);
  const [currentTaskIDs, setCurrentTaskIDs] = useState<string[]>([]);
  const [taskView, setTaskView] = useState<ImportTaskView>("history");
  const tasksButton = useRef<HTMLButtonElement>(null);
  const previewTrigger = useRef<HTMLButtonElement | null>(null);
  const [destination, setDestination] = useState("");
  const [layout, setLayout] = useState("date");
  const [plan, setPlan] = useState<Plan | null>(null);
  const [preview, setPreview] = useState<Media | null>(null);
  const [disconnectOpen, setDisconnectOpen] = useState(false);
  const actionLock = useRef(false);
  const connectButton = useRef<HTMLButtonElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const libraryKey = useRef("");
  const connection = state.connection;
  const connected = connection.phase === "connected";
  const busy = acting || connection.busy;
  const pending = state.tasks.filter(
    (t) => t.status === "queued" || t.status === "downloading",
  );
  const provider = state.providers.find((p) => p.id === state.activeProvider);
  const enabled = isDesktop && !demo && Boolean(provider?.available);

  useEffect(() => {
    onPending(pending.length);
  }, [pending.length, onPending]);
  useEffect(() => {
    if (composing) return;
    const timer = window.setTimeout(() => {
      setSearch(searchText);
      setPage(1);
    }, 300);
    return () => clearTimeout(timer);
  }, [searchText, composing]);

  useEffect(() => {
    if (demo || !isDesktop) return;
    let active = true;
    let timer: number;
    let failures = 0;
    async function poll() {
      try {
        const next = await devicesAPI.state();
        if (!active) return;
        setState(next);
        const key = `${next.activeProvider}/${next.connection.deviceId}/${next.connection.revision}`;
        if (
          next.connection.phase === "connected" &&
          key !== libraryKey.current
        ) {
          setLoading(true);
          const files = await devicesAPI.library();
          if (!active) return;
          // 新会话清空选择；刷新同一设备只剔除消失的条目。
          const sameDevice = libraryKey.current.startsWith(
            `${next.activeProvider}/${next.connection.deviceId}/`,
          );
          setSelected((old) =>
            sameDevice
              ? new Set([...old].filter((id) => files.some((f) => f.id === id)))
              : new Set(),
          );
          setMedia(files);
          setPage(1);
          libraryKey.current = key;
          setLoading(false);
        } else if (next.connection.phase === "disconnected") {
          setMedia([]);
          setSelected(new Set());
          setPreview(null);
          libraryKey.current = "";
        }
        failures = 0;
        setMonitorError("");
        timer = window.setTimeout(
          poll,
          next.connection.busy ||
            next.tasks.some(
              (t) => t.status === "queued" || t.status === "downloading",
            )
            ? 600
            : 1800,
        );
      } catch (e) {
        if (!active) return;
        setLoading(false);
        failures++;
        setMonitorError(
          `暂时无法获取设备状态，已保留当前内容。${errorMessage(e)}`,
        );
        if (failures < 5)
          timer = window.setTimeout(poll, Math.min(8000, 1000 * 2 ** failures));
      }
    }
    void poll();
    return () => {
      active = false;
      clearTimeout(timer);
    };
  }, [revision, demo]);

  async function run(action: () => Promise<void>) {
    if (actionLock.current) return;
    actionLock.current = true;
    setActing(true);
    setError("");
    setNotice("");
    try {
      await action();
      setRevision((n) => n + 1);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      actionLock.current = false;
      setActing(false);
    }
  }
  useEffect(() => {
    setPreview(null);
  }, [connection.sessionToken]);
  const imported = useMemo(
    () =>
      new Set(
        state.tasks
          .filter(
            (t) =>
              t.status === "completed" &&
              t.deviceId === connection.deviceId &&
              t.providerId === state.activeProvider &&
              t.root === destination &&
              t.layout === layout,
          )
          .map((t) => `${t.media.id}/${t.media.date}/${t.media.size}`),
      ),
    [
      state.tasks,
      connection.deviceId,
      state.activeProvider,
      destination,
      layout,
    ],
  );
  const rangeError = Boolean(from && to && from > to);
  const filtered = useMemo(
    () =>
      sortByCaptureDate(
        media.filter((f) => {
          const day = captureDate(f.date)?.slice(0, 10);
          return (
            (!search ||
              `${f.name} ${f.path}`
                .toLocaleLowerCase()
                .includes(search.toLocaleLowerCase())) &&
            (kind === "all" || f.kind === kind) &&
            (storage === "all" || f.storageId === storage) &&
            (!from || Boolean(day && day >= from)) &&
            (!to || Boolean(day && day <= to)) &&
            (!onlyNew || !imported.has(`${f.id}/${f.date}/${f.size}`))
          );
        }),
      ),
    [media, search, kind, storage, from, to, onlyNew, imported],
  );
  const pages = Math.max(1, Math.ceil(filtered.length / 30));
  const visible = filtered.slice(
    (Math.min(page, pages) - 1) * 30,
    Math.min(page, pages) * 30,
  );
  const picked = media.filter((f) => selected.has(f.id));
  const pickedBytes = picked.reduce((n, f) => n + f.size, 0);
  const filteredPicked = filtered.filter((f) => selected.has(f.id)).length;
  const storageNames = new Map(
    (provider?.storages ?? []).map((item) => [item.id, item.name]),
  );
  for (const file of media)
    if (!storageNames.has(file.storageId))
      storageNames.set(file.storageId, file.storageName);
  const storageOptions = [...storageNames.entries()];
  useEffect(() => {
    setPage((value) => Math.min(value, pages));
  }, [pages]);
  function toggle(ids: string[], checked: boolean) {
    setSelected((old) =>
      setMediaSelection(
        old,
        ids.map((id) => ({ id })),
        checked,
      ),
    );
  }
  function openTasks() {
    setError("");
    setNotice("");
    setTasksOpen(true);
  }
  const resetFilters = () => {
    setSearchText("");
    setSearch("");
    setKind("all");
    setStorage("all");
    setFrom("");
    setTo("");
    setOnlyNew(false);
    setPage(1);
  };

  return (
    <div className="device-page">
      {(!enabled || demo) && (
        <div className="message" role="status">
          <Camera size={18} />
          <span>
            {demo
              ? "界面演示模式不会连接设备或导入原片。请退出演示后使用桌面客户端。"
              : "当前为浏览器预览；无线连接与文件导入请在 macOS 桌面客户端中使用。"}
          </span>
        </div>
      )}
      {error && (
        <div className="message error" role="alert">
          <span>{error}</span>
          <Button
            variant="ghost"
            size="icon"
            aria-label="关闭设备操作错误"
            onClick={() => setError("")}
          >
            <X size={16} />
          </Button>
        </div>
      )}
      {notice && (
        <div className="message" role="status">
          <CheckCheck size={18} />
          <span>{notice}</span>
        </div>
      )}
      {(monitorError || state.warning || connection.warning) && (
        <div className="message error" role="alert">
          <span>{monitorError || state.warning || connection.warning}</span>
          {monitorError && (
            <Button
              variant="outline"
              size="sm"
              onClick={() => setRevision((n) => n + 1)}
            >
              重新获取状态
            </Button>
          )}
        </div>
      )}

      <div className="device-page-actions">
        <span>浏览设备原片，确认后再导入。</span>
        <Button
          ref={tasksButton}
          variant="outline"
          aria-label="导入任务"
          onClick={openTasks}
        >
          {pending.length ? (
            <LoaderCircle className="spin" size={16} />
          ) : (
            <History size={16} />
          )}
          导入任务
          {state.tasks.length > 0 && (
            <span className="device-task-entry-count">
              {pending.length
                ? `${pending.length} 项进行中`
                : `${state.tasks.length} 条记录`}
            </span>
          )}
        </Button>
      </div>
      <section
        className="device-source-panel"
        aria-labelledby="device-source-heading"
      >
        <div className="device-source-identity">
          <span className="device-symbol">
            <Camera size={30} />
          </span>
          <div>
            <div className="device-overline" id="device-source-heading">
              当前导入设备
            </div>
            <h2>{connection.deviceName || provider?.name || "摄影设备"}</h2>
            <p>
              {connected ? (
                <>
                  <Wifi size={13} /> {connection.ssid || "设备直连"}
                </>
              ) : (
                provider?.transport
              )}
            </p>
          </div>
        </div>
        <div className="device-source-actions">
          <Badge variant="outline">
            {phaseLabel[connection.phase] || connection.phase}
          </Badge>
          {connected || connection.phase === "lost" ? (
            <>
              <Button
                variant="outline"
                disabled={busy || pending.length > 0}
                onClick={() => void run(() => devicesAPI.refresh())}
              >
                <RefreshCw size={16} />
                刷新素材
              </Button>
              <Button
                variant="ghost"
                disabled={acting}
                onClick={() => setDisconnectOpen(true)}
              >
                <Unplug size={16} />
                断开
              </Button>
            </>
          ) : (
            <Button
              ref={connectButton}
              disabled={!enabled || acting}
              onClick={() => setConnectionOpen(true)}
            >
              <Bluetooth size={16} />
              {connection.phase === "disconnected" ? "连接设备" : "继续连接"}
            </Button>
          )}
        </div>
        <div className="device-source-note">
          <span>{provider?.compatibility}</span>
          <span>设备原片只读 · 导入后不删除</span>
        </div>
      </section>

      {!connected && media.length === 0 ? (
        <section className="device-welcome">
          <div className="device-intro">
            <div className="device-overline">从拍摄现场，回到创作工作台</div>
            <h2>
              把原片带回来，
              <br />
              让整理顺势开始。
            </h2>
            <p>
              先连接设备，再按日期和存储位置挑选素材。原始画质、文件名和拍摄时间都值得保留。
            </p>
            <ol className="device-workflow">
              <li>
                <span>01</span>
                <div>
                  <strong>连接你的设备</strong>
                  <small>蓝牙配对，确认后加入相机热点</small>
                </div>
              </li>
              <li>
                <span>02</span>
                <div>
                  <strong>挑选这次拍摄</strong>
                  <small>浏览原片，按照片、视频与日期筛选</small>
                </div>
              </li>
              <li>
                <span>03</span>
                <div>
                  <strong>导入并检查结果</strong>
                  <small>保留原片，支持暂停、续传与失败重试</small>
                </div>
              </li>
            </ol>
          </div>
          <div className="device-entry-options">
            <div className="device-option-heading">
              <Camera size={22} />
              <h3>无线设备导入</h3>
            </div>
            <p>
              本次支持 DJI Osmo Action 6。请保持设备开机，并先断开手机 DJI
              Mimo。
            </p>
            <div className="device-capability">
              <span>连接方式</span>
              <strong>蓝牙 + Wi-Fi</strong>
              <span>素材来源</span>
              <strong>内置存储 / SD 卡</strong>
              <span>素材类型</span>
              <strong>照片 / DNG / 视频</strong>
            </div>
            <p className="device-footnote">
              加入相机热点时，电脑的 Wi-Fi 互联网连接可能暂时中断。
            </p>
            <div className="device-local-entry">
              <HardDrive size={21} />
              <div>
                <h3>存储卡、USB 磁盘或本地文件夹</h3>
                <p>已挂载的设备可直接使用现有素材分析。</p>
              </div>
              <Button
                variant="ghost"
                size="icon"
                aria-label="进入本地素材分析"
                onClick={onLocal}
              >
                <ArrowRight size={19} />
              </Button>
            </div>
            <p className="device-footnote">
              DJI
              无人机、索尼相机等无线适配将后续扩展；目前可通过已挂载存储导入。
            </p>
          </div>
        </section>
      ) : (
        <div className="device-import-layout">
          <section
            className="device-library"
            aria-labelledby="device-library-heading"
          >
            <div className="device-panel-heading">
              <div>
                <h2 id="device-library-heading">设备素材</h2>
                <p>
                  共 {number(media.length)} 项 · 照片{" "}
                  {number(media.filter((f) => f.kind === "photo").length)} ·
                  视频 {number(media.filter((f) => f.kind === "video").length)}
                </p>
              </div>
              {loading && (
                <LoaderCircle
                  className="spin"
                  size={19}
                  aria-label="正在读取素材"
                />
              )}
            </div>
            <div className="device-filters">
              <div className="device-search">
                <Search size={16} />
                <Input
                  ref={searchRef}
                  aria-label="搜索设备素材"
                  placeholder="搜索文件名或路径"
                  value={searchText}
                  onCompositionStart={() => setComposing(true)}
                  onCompositionEnd={() => setComposing(false)}
                  onChange={(e) => setSearchText(e.target.value)}
                />
                {searchText && (
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label="清空设备素材搜索"
                    onClick={() => {
                      setSearchText("");
                      setSearch("");
                      setPage(1);
                      searchRef.current?.focus();
                    }}
                  >
                    <X size={15} />
                  </Button>
                )}
              </div>
            </div>
            <div className="device-filter-row">
              <span>素材类型</span>
              <div
                className="device-segmented"
                role="group"
                aria-label="设备素材类型"
              >
                {[
                  ["all", "全部"],
                  ["photo", "图片"],
                  ["video", "视频"],
                ].map(([value, label]) => (
                  <Button
                    key={value}
                    size="sm"
                    variant="ghost"
                    aria-pressed={kind === value}
                    onClick={() => {
                      setKind(value);
                      setPage(1);
                    }}
                  >
                    {label}
                  </Button>
                ))}
              </div>
            </div>
            <div className="device-filter-row">
              <span>存储位置</span>
              <div
                className="device-segmented"
                role="group"
                aria-label="设备存储位置"
              >
                {[["all", "全部"], ...storageOptions].map(([value, label]) => (
                  <Button
                    key={value}
                    size="sm"
                    variant="ghost"
                    aria-pressed={storage === value}
                    onClick={() => {
                      setStorage(value);
                      setPage(1);
                    }}
                  >
                    {label}
                  </Button>
                ))}
              </div>
            </div>
            <div className="device-date-filters">
              <label>
                开始日期
                <Input
                  type="date"
                  value={from}
                  aria-invalid={rangeError}
                  aria-describedby={
                    rangeError ? "device-date-error" : undefined
                  }
                  onChange={(e) => {
                    setFrom(e.target.value);
                    setPage(1);
                  }}
                />
              </label>
              <span>至</span>
              <label>
                结束日期
                <Input
                  type="date"
                  value={to}
                  aria-invalid={rangeError}
                  aria-describedby={
                    rangeError ? "device-date-error" : undefined
                  }
                  onChange={(e) => {
                    setTo(e.target.value);
                    setPage(1);
                  }}
                />
              </label>
              <Button size="sm" variant="ghost" onClick={resetFilters}>
                重置筛选
              </Button>
            </div>
            {rangeError && (
              <p
                className="device-field-error"
                id="device-date-error"
                role="alert"
              >
                开始日期不能晚于结束日期，请调整日期范围。
              </p>
            )}
            <div className="device-selection-bar">
              <label>
                <Checkbox
                  checked={mediaSelectionState(selected, visible)}
                  disabled={!visible.length || rangeError}
                  onCheckedChange={(v) =>
                    toggle(
                      visible.map((f) => f.id),
                      v === true,
                    )
                  }
                />
                选择当前页
              </label>
              <Button
                variant="ghost"
                size="sm"
                disabled={!filtered.length || rangeError}
                onClick={() =>
                  toggle(
                    filtered.map((f) => f.id),
                    true,
                  )
                }
              >
                选择全部筛选结果
              </Button>
              <span>{number(filtered.length)} 项</span>
            </div>
            <DeviceMediaBrowser
              files={filtered}
              visible={visible}
              page={page}
              selected={selected}
              imported={imported}
              loading={loading}
              hasMedia={media.length > 0}
              disabled={rangeError}
              storageNames={storageNames}
              provider={state.activeProvider}
              session={connection.sessionToken}
              revision={connection.revision}
              connected={connected}
              onToggle={toggle}
              onPreview={(file, trigger) => {
                previewTrigger.current = trigger;
                setPreview(file);
              }}
            />
            <div className="device-pagination">
              <span>
                第 {Math.min(page, pages)} / {pages} 页 · 每页 30 项
              </span>
              <Button
                variant="ghost"
                size="icon"
                aria-label="设备素材上一页"
                disabled={page <= 1}
                onClick={() => setPage((n) => n - 1)}
              >
                <ChevronLeft size={17} />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                aria-label="设备素材下一页"
                disabled={page >= pages}
                onClick={() => setPage((n) => n + 1)}
              >
                <ChevronRight size={17} />
              </Button>
            </div>
          </section>
          <aside
            className="device-import-panel"
            aria-labelledby="device-plan-heading"
          >
            <div className="device-panel-heading">
              <h2 id="device-plan-heading">这次导入</h2>
              <ArrowDownToLine size={20} />
            </div>
            <div className="device-selection-total">
              <strong>
                {number(picked.length)}
                <small> 项素材</small>
              </strong>
              <span>清单估计 {bytes(pickedBytes)}</span>
            </div>
            <p className="device-footnote">
              {picked.length - filteredPicked > 0
                ? `其中 ${picked.length - filteredPicked} 项在当前筛选之外，仍会一并导入。`
                : "筛选和翻页会保留选择，导入按全部已选素材执行。"}
            </p>
            <Button
              variant="ghost"
              size="sm"
              disabled={!selected.size}
              onClick={() => setSelected(new Set())}
            >
              清空全部选择
            </Button>
            <label className="device-field-label" htmlFor="device-destination">
              导入位置
            </label>
            <div className="device-destination">
              <Input
                id="device-destination"
                placeholder="选择文件夹或粘贴绝对路径"
                value={destination}
                onChange={(e) => setDestination(e.target.value)}
              />
              <Button
                variant="outline"
                size="icon"
                aria-label="选择设备素材导入文件夹"
                disabled={!enabled || busy}
                onClick={() =>
                  void run(async () => {
                    const path = await devicesAPI.choose();
                    if (path) setDestination(path);
                  })
                }
              >
                <FolderOpen size={18} />
              </Button>
            </div>
            <label className="device-field-label" htmlFor="device-layout">
              目录组织方式
            </label>
            <DeviceSelect value={layout} onValueChange={setLayout}>
              <SelectTrigger id="device-layout">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="date">按拍摄日期归档</SelectItem>
                <SelectItem value="source">保留设备原目录</SelectItem>
              </SelectContent>
            </DeviceSelect>
            <div className="device-folder-example">
              {layout === "date" ? (
                <>
                  photo / 年 / 月 / 日 / 原文件名
                  <br />
                  video / 年 / 月 / 日 / 原文件名
                </>
              ) : (
                <>设备原目录 / 设备标识 / 存储 / 原路径</>
              )}
            </div>
            <label className="device-new-filter">
              <Checkbox
                checked={onlyNew}
                disabled={!destination}
                onCheckedChange={(v) => {
                  setOnlyNew(v === true);
                  setPage(1);
                }}
              />
              隐藏此位置已有导入记录的素材
            </label>
            <p className="device-footnote">
              依据本机记录筛选；提交时检查目标文件是否仍存在。不会按文件名认定内容重复。
            </p>
            <Button
              className="w-full"
              disabled={
                !enabled ||
                !connected ||
                !picked.length ||
                !destination.trim() ||
                busy ||
                rangeError
              }
              onClick={() =>
                void run(async () =>
                  setPlan(
                    await devicesAPI.prepare(
                      [...selected],
                      destination,
                      layout,
                    ),
                  ),
                )
              }
            >
              {acting ? <LoaderCircle className="spin" /> : <ArrowDownToLine />}
              预览导入
            </Button>
            <div className="device-original-note">
              <ShieldCheck size={17} />
              <span>
                保留设备原片，不转码。重名文件自动编号；未知日期单独归档。
              </span>
            </div>
          </aside>
        </div>
      )}

      <DeviceImportTaskDialog
        open={tasksOpen}
        onOpenChange={setTasksOpen}
        tasks={state.tasks}
        taskIDs={currentTaskIDs}
        view={taskView}
        onViewChange={setTaskView}
        connection={connection}
        providerID={state.activeProvider}
        busy={busy}
        error={error}
        notice={notice}
        monitorError={monitorError}
        warning={state.warning}
        returnFocus={tasksButton}
        onRefresh={() => setRevision((n) => n + 1)}
        onPause={() => void run(() => devicesAPI.pause())}
        onResume={() =>
          void run(async () => {
            const count = await devicesAPI.resume();
            setNotice(`已恢复 ${count} 项导入，其余任务请查看具体原因。`);
          })
        }
        onRetry={(id) => void run(() => devicesAPI.retry(id))}
        onReveal={(id) => void run(() => devicesAPI.reveal(id))}
        onAnalyze={(path) => {
          setTasksOpen(false);
          onAnalyze(path);
        }}
      />

      <DeviceConnectionDialog
        open={connectionOpen}
        onOpenChange={setConnectionOpen}
        state={connection}
        onState={(next) => {
          setState((old) => ({ ...old, connection: next }));
          setRevision((n) => n + 1);
        }}
        returnFocus={connectButton}
      />
      <Dialog
        open={Boolean(plan)}
        onOpenChange={(open) => {
          if (!open && !acting) setPlan(null);
        }}
      >
        <DialogContent
          className="device-plan-dialog max-h-[88vh] overflow-y-auto"
          onCloseAutoFocus={(event) => {
            if (tasksOpen) event.preventDefault();
          }}
        >
          <DialogHeader>
            <DialogTitle>确认导入 {plan?.count} 项原始素材</DialogTitle>
            <DialogDescription>
              {plan?.deviceName} · 清单估计 {bytes(plan?.bytes || 0)}
              。导入只读取设备文件。
            </DialogDescription>
          </DialogHeader>
          <dl className="device-detail-list">
            <dt>保存位置</dt>
            <dd>{plan?.destination}</dd>
            <dt>组织方式</dt>
            <dd>
              {plan?.layout === "date" ? "按拍摄日期归档" : "保留设备原目录"}
            </dd>
          </dl>
          {Boolean(plan?.skipped) && (
            <p>已跳过 {plan?.skipped} 项已有文件或队列中的任务。</p>
          )}
          {Boolean(plan?.unknownDates) && (
            <p>{plan?.unknownDates} 项没有有效日期，将放入“日期未知”目录。</p>
          )}
          <div className="device-folder-example">
            {plan?.examples.map((path) => (
              <div key={path}>{path}</div>
            ))}
          </div>
          <p className="device-footnote">
            完成后检查下载长度并生成本地 SHA-256
            指纹。设备未提供源端哈希，不能据此认定完成源端哈希核验。
          </p>
          {error && (
            <p className="device-field-error" role="alert">
              {error}
            </p>
          )}
          <DialogFooter>
            <Button
              variant="outline"
              disabled={acting}
              onClick={() => setPlan(null)}
            >
              返回调整
            </Button>
            <Button
              disabled={acting || !plan?.count || !connected}
              onClick={() =>
                void run(async () => {
                  if (!plan) return;
                  await devicesAPI.start(plan.id);
                  const ids = plan.taskIds;
                  setCurrentTaskIDs(ids);
                  setTaskView("current");
                  setState((old) => ({
                    ...old,
                    tasks: old.tasks.map((task) =>
                      ids.includes(task.id)
                        ? { ...task, status: "queued", error: "" }
                        : task,
                    ),
                  }));
                  setPlan(null);
                  setTasksOpen(true);
                  setSelected(new Set());
                  setNotice(
                    "素材已加入导入队列。关闭弹窗后仍可从“导入任务”查看进度和结果。",
                  );
                })
              }
            >
              {acting ? <LoaderCircle className="spin" /> : <ArrowDownToLine />}
              开始导入
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Dialog open={disconnectOpen} onOpenChange={setDisconnectOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>断开当前设备？</DialogTitle>
            <DialogDescription>
              未完成的导入将暂停并保留续传片段。若由应用切换
              Wi-Fi，将尝试恢复原网络。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              autoFocus
              onClick={() => setDisconnectOpen(false)}
            >
              保持连接
            </Button>
            <Button
              disabled={acting}
              onClick={() =>
                void run(async () => {
                  await devicesAPI.disconnect();
                  setDisconnectOpen(false);
                  setNotice("设备已断开，已完成的文件和导入记录均已保留。");
                })
              }
            >
              暂停并断开
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <DevicePreview
        file={preview}
        provider={state.activeProvider}
        session={connection.sessionToken}
        connected={connected}
        onClose={() => setPreview(null)}
        returnFocus={previewTrigger}
      />
    </div>
  );
}

function DevicePreview({
  file,
  provider,
  connected,
  onClose,
  session,
  returnFocus,
}: {
  file: Media | null;
  provider: string;
  session: string;
  connected: boolean;
  onClose: () => void;
  returnFocus: { current: HTMLButtonElement | null };
}) {
  const [failed, setFailed] = useState(false);
  useEffect(() => setFailed(false), [file?.id]);
  const raw = file?.name.toLowerCase().endsWith(".dng");
  return (
    <Dialog
      open={Boolean(file)}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent
        className="sm:max-w-[800px] max-h-[90vh] overflow-y-auto"
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          returnFocus.current?.focus();
        }}
      >
        <DialogHeader>
          <DialogTitle className="break-all">{file?.name}</DialogTitle>
          <DialogDescription>
            {file?.storageName} · {file?.date || "日期未知"} · 清单容量{" "}
            {bytes(file?.size || 0)}
          </DialogDescription>
        </DialogHeader>
        <div className="device-preview">
          {file && connected && !failed ? (
            file.kind === "video" ? (
              <video
                controls
                preload="metadata"
                src={deviceMediaURL(provider, file.id, true, session)}
                onError={() => setFailed(true)}
              />
            ) : (
              <img
                src={deviceMediaURL(provider, file.id, !raw, session)}
                alt={raw ? "DNG 相机缩略图" : file.name}
                onError={() => setFailed(true)}
              />
            )
          ) : (
            <p>
              {connected
                ? "无法预览此素材，请导入原文件后使用本机应用查看。"
                : "设备连接已中断，重新连接后可继续预览。"}
            </p>
          )}
        </div>
        <p className="device-footnote">
          {raw
            ? "DNG 只显示设备缩略图；导入时保存原始 DNG。"
            : file?.kind === "video"
              ? "优先播放设备低分辨率预览，缺失时读取原视频；不自动转码。"
              : "预览按需读取设备中的照片。"}{" "}
          预览不代表原片已经保存。
        </p>
        <p className="device-source-path">{file?.path}</p>
      </DialogContent>
    </Dialog>
  );
}

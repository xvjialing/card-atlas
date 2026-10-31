import { useEffect, useMemo, useRef, useState } from "react";
import {
  Aperture,
  ArrowDownToLine,
  ArrowRight,
  CalendarDays,
  Camera,
  Check,
  CheckCheck,
  ChevronLeft,
  ChevronRight,
  CircleHelp,
  FileImage,
  FileText,
  Film,
  Folder,
  FolderOpen,
  HardDrive,
  History,
  Layers,
  LoaderCircle,
  RotateCcw,
  Search,
  ShieldCheck,
  SlidersHorizontal,
  Sparkles,
  X,
  TriangleAlert,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select as MediaSelect,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { api, isDesktop } from "@/lib/api";
import { DeviceImportPage } from "@/components/device-import-page";
import { DiagnosticsPage } from "@/components/diagnostics-page";
import { MediaThumbnail } from "@/components/media-thumbnail";
import { ExportTaskDialog } from "@/components/export-task-dialog";
import { exportProgress, exportTitle } from "@/lib/jobs";
import { demoSummary, filteredDemo, demoPlan } from "@/lib/demo";
import {
  bytes,
  number,
  emptySummary,
  idleJob,
  type Summary,
  type Job,
  type FilePage,
  type Filter,
  type Plan,
  type MediaFile,
} from "@/lib/types";

const initialFilter: Filter = {
  search: "",
  kind: "all",
  from: "",
  to: "",
  page: 1,
  sort: "date",
};
const noFiles: FilePage = { files: [], total: 0, bytes: 0 };
const errorText = (e: unknown) => (e instanceof Error ? e.message : String(e));
const dateLabel = (date: string) => date.replaceAll("-", ".");

export default function App() {
  const [demo, setDemo] = useState(
    new URLSearchParams(location.search).get("demo") === "1",
  );
  const [summary, setSummary] = useState<Summary>(emptySummary);
  const [source, setSource] = useState("");
  const [destination, setDestination] = useState("");
  const [job, setJob] = useState<Job>(idleJob);
  const [filter, setFilter] = useState<Filter>(initialFilter);
  const [searchText, setSearchText] = useState("");
  const [composing, setComposing] = useState(false);
  const [result, setResult] = useState<FilePage>(noFiles);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [plan, setPlan] = useState<Plan | null>(null);
  const [view, setView] = useState<
    "library" | "devices" | "history" | "help" | "diagnostics"
  >("library");
  const [devicePending, setDevicePending] = useState(0);
  const [history, setHistory] = useState<Job[]>([]);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [acting, setActing] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [exportOpen, setExportOpen] = useState(false);
  const [monitorError, setMonitorError] = useState("");
  const [monitorRevision, setMonitorRevision] = useState(0);
  const [detail, setDetail] = useState<MediaFile | null>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const actionInFlight = useRef(false);
  const busy = job.state === "running" || acting;
  const loaded = Boolean(summary.path);

  useEffect(() => {
    if (demo) {
      const s = demoSummary();
      setSummary(s);
      setSource(s.path);
      return;
    }
    if (isDesktop)
      Promise.all([api.summary(), api.status(), api.history()])
        .then(([s, currentJob, records]) => {
          setSummary(s);
          setSource(s.path);
          setJob(currentJob);
          setHistory(records);
          if (currentJob.kind === "export") setExportOpen(true);
        })
        .catch((e) => setError(errorText(e)));
  }, [demo]);

  useEffect(() => {
    if (composing) return;
    const timer = window.setTimeout(
      () => setFilter((q) => ({ ...q, search: searchText, page: 1 })),
      300,
    );
    return () => clearTimeout(timer);
  }, [searchText, composing]);

  useEffect(() => {
    let active = true;
    if (!loaded) {
      setResult(noFiles);
      return;
    }
    setLoading(true);
    const query = demo
      ? Promise.resolve(
          (() => {
            const files = filteredDemo(filter);
            return {
              files: files.slice((filter.page - 1) * 30, filter.page * 30),
              total: files.length,
              bytes: files.reduce((n, f) => n + f.size, 0),
            };
          })(),
        )
      : api.query(filter);
    query
      .then((r) => {
        if (active) {
          setResult(r);
          const pages = Math.max(1, Math.ceil(r.total / 30));
          if (filter.page > pages) setFilter((q) => ({ ...q, page: pages }));
        }
      })
      .catch((e) => {
        if (active) setError(errorText(e));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [filter, summary, loaded, demo]);

  useEffect(() => {
    let active = true;
    if (!selected.size) {
      setPlan(null);
      return;
    }
    const p = demo
      ? Promise.resolve(demoPlan([...selected]))
      : api.plan([...selected]);
    p.then((p) => {
      if (active) setPlan(p);
    }).catch((e) => {
      if (active) setError(errorText(e));
    });
    return () => {
      active = false;
    };
  }, [selected, demo]);

  useEffect(() => {
    if (job.state !== "running" || demo) return;
    let active = true;
    let timer: number;
    let failures = 0;
    const poll = async () => {
      try {
        const next = await api.status();
        if (!active) return;
        if (next.id !== job.id) {
          setMonitorError("当前任务状态已改变，请重新获取进度。");
          return;
        }
        failures = 0;
        setMonitorError("");
        if (next.state === "completed" && next.kind === "scan") {
          const s = await api.summary();
          if (!active) return;
          setSummary(s);
          setSelected(new Set());
          setFilter(initialFilter);
          setSearchText("");
          setNotice(`分析完成，找到 ${number(s.count)} 个素材`);
        }
        if (next.state !== "running" && next.kind === "export") {
          // 先完成异步结果读取，再提交终态，避免 effect 清理丢失结果反馈。
          try {
            const h = await api.history();
            if (active) setHistory(h);
          } catch (e) {
            if (active) setError(`读取导出记录失败：${errorText(e)}`);
          }
          if (!active) return;
          setNotice(
            `${exportTitle(next)}：${next.copied} 个成功，${next.failed} 个失败`,
          );
        }
        if (!active) return;
        setJob(next);
        if (next.state === "failed")
          setError(next.errors.join("；") || "任务失败，请重试");
        if (next.state === "running") timer = window.setTimeout(poll, 400);
      } catch (e) {
        if (active) {
          failures++;
          setMonitorError(
            `暂时无法获取进度，任务可能仍在继续：${errorText(e)}`,
          );
          if (failures < 5)
            timer = window.setTimeout(poll, Math.min(4000, failures * 1000));
        }
      }
    };
    timer = window.setTimeout(poll, 100);
    return () => {
      active = false;
      clearTimeout(timer);
    };
  }, [job.id, job.state, demo, monitorRevision]);

  const changeFilter = (patch: Partial<Filter>) =>
    setFilter((q) => ({ ...q, ...patch, page: 1 }));
  const resetFilters = () => {
    setSearchText("");
    setFilter(initialFilter);
  };
  const toggle = (id: string) =>
    setSelected((old) => {
      const next = new Set(old);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  const pageSelected =
    result.files.length > 0 && result.files.every((f) => selected.has(f.id));
  const pagePartial =
    result.files.some((f) => selected.has(f.id)) && !pageSelected;
  const togglePage = () =>
    setSelected((old) => {
      const next = new Set(old);
      for (const f of result.files)
        pageSelected ? next.delete(f.id) : next.add(f.id);
      return next;
    });
  const run = async (fn: () => Promise<void>) => {
    if (actionInFlight.current) return;
    actionInFlight.current = true;
    setActing(true);
    setError("");
    try {
      await fn();
    } catch (e) {
      setError(errorText(e));
    } finally {
      actionInFlight.current = false;
      setActing(false);
    }
  };
  const scan = () =>
    run(async () => {
      if (demo) {
        setNotice("当前为演示数据，请退出演示后使用桌面客户端分析文件夹");
        return;
      }
      if (!isDesktop)
        throw new Error(
          "请打开 CardAtlas 桌面客户端来分析本地文件夹，或使用演示模式预览界面",
        );
      setNotice("");
      setJob(await api.scan(source));
    });
  const chooseSource = () =>
    run(async () => {
      if (!isDesktop) {
        throw new Error("本地文件夹选择需要在 CardAtlas 桌面客户端中使用");
      }
      const path = await api.choose(false);
      if (path) {
        setSource(path);
        setNotice("");
        setJob(await api.scan(path));
      }
    });
  const chooseDestination = () =>
    run(async () => {
      if (demo) {
        setDestination("/演示/影像资料库");
        return;
      }
      if (!isDesktop) throw new Error("请在桌面客户端中选择导出文件夹");
      const path = await api.choose(true);
      if (path) setDestination(path);
    });
  const selectAll = () =>
    run(async () => {
      const ids = demo
        ? filteredDemo(filter).map((f) => f.id)
        : await api.ids(filter);
      setSelected((old) => new Set([...old, ...ids]));
    });
  const exportFiles = () =>
    run(async () => {
      if (demo) {
        setConfirm(false);
        setNotice("演示模式仅预览整理规则，不会复制真实文件");
        return;
      }
      setConfirm(false);
      setExportOpen(true);
      setNotice("");
      setMonitorError("");
      setJob({
        ...idleJob,
        kind: "export",
        state: "starting",
        total: selected.size,
        totalBytes: plan?.bytes || 0,
        totalWork: (plan?.bytes || 0) * 2,
        destination,
      });
      try {
        const next = await api.export(destination, [...selected]);
        setJob(next);
      } catch (e) {
        setJob({
          ...idleJob,
          kind: "export",
          state: "failed",
          total: selected.size,
          destination,
          errors: [errorText(e)],
        });
      }
    });
  const leaveDemo = () => {
    setDemo(false);
    setSummary(emptySummary);
    setSource("");
    setDestination("");
    setResult(noFiles);
    setSelected(new Set());
    setFilter(initialFilter);
    setSearchText("");
    setNotice("");
    setError("");
  };
  const showHistory = () => {
    setView("history");
    if (isDesktop && !demo)
      api
        .history()
        .then(setHistory)
        .catch((e) => setError(errorText(e)));
  };
  const pages = Math.max(1, Math.ceil(result.total / 30));
  const filtered =
    filter.kind !== "all" || filter.from || filter.to || filter.search;
  const rangeError = Boolean(
    filter.from && filter.to && filter.from > filter.to,
  );
  const chartDays = useMemo(() => {
    if (summary.days.length <= 24)
      return summary.days.map((d) => ({
        ...d,
        label: d.date.slice(5),
        from: d.date,
        to: d.date,
      }));
    const months = new Map<
      string,
      {
        date: string;
        label: string;
        photos: number;
        videos: number;
        bytes: number;
        from: string;
        to: string;
      }
    >();
    for (const d of summary.days) {
      const key = d.date.slice(0, 7);
      const m = months.get(key) || {
        date: key,
        label: key,
        photos: 0,
        videos: 0,
        bytes: 0,
        from: d.date,
        to: d.date,
      };
      m.photos += d.photos;
      m.videos += d.videos;
      m.bytes += d.bytes;
      m.to = d.date;
      months.set(key, m);
    }
    return [...months.values()];
  }, [summary.days]);
  const maxDay = Math.max(1, ...chartDays.map((d) => d.photos + d.videos));

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <button
          className="brand"
          onClick={() => setView("library")}
          aria-label="CardAtlas 影像归档首页"
        >
          <span className="brand-icon">
            <img src="/cardatlas.png" width={43} height={43} alt="" />
          </span>
          <span>
            CardAtlas<small>影像归档工作台</small>
          </span>
        </button>
        <div className="sidebar-label">工作空间</div>
        <nav aria-label="主导航">
          <button
            className={`nav-item ${view === "library" ? "active" : ""}`}
            onClick={() => setView("library")}
            aria-current={view === "library" ? "page" : undefined}
          >
            <Layers size={19} />
            素材分析
            <span className="nav-dot" />
          </button>
          <button
            className={`nav-item ${view === "devices" ? "active" : ""}`}
            onClick={() => setView("devices")}
            aria-current={view === "devices" ? "page" : undefined}
          >
            <Camera size={19} />
            设备导入
            {devicePending > 0 && (
              <span
                className="nav-count"
                aria-label={`${devicePending} 项导入中`}
              >
                {devicePending}
              </span>
            )}
          </button>
          <button
            className={`nav-item ${view === "history" ? "active" : ""}`}
            onClick={showHistory}
            aria-current={view === "history" ? "page" : undefined}
          >
            <History size={19} />
            导出记录
            {history.length > 0 && (
              <span className="nav-count">{history.length}</span>
            )}
          </button>
          <button
            className={`nav-item ${view === "diagnostics" ? "active" : ""}`}
            onClick={() => setView("diagnostics")}
            aria-current={view === "diagnostics" ? "page" : undefined}
          >
            <FileText size={19} />
            诊断日志
          </button>
          <button
            className={`nav-item ${view === "help" ? "active" : ""}`}
            onClick={() => setView("help")}
            aria-current={view === "help" ? "page" : undefined}
          >
            <CircleHelp size={19} />
            使用说明
          </button>
        </nav>
        <div className="sidebar-card">
          <ShieldCheck size={23} />
          <strong>让每一份影像，各归其位。</strong>
          <p>按拍摄日期整理照片与视频，保留原始文件与画质。</p>
          <span>本地处理 · 无需上传</span>
        </div>
        <div className="sidebar-bottom">
          <span className="status-dot" />
          {demo
            ? "界面演示模式"
            : isDesktop
              ? "本地客户端已就绪"
              : "浏览器预览"}
          <span className="version">v0.1.0</span>
        </div>
      </aside>

      <main className="main-area">
        <header className="topbar">
          <span>
            <span className="crumb">工作空间</span>
            <ChevronRight size={14} />
            {view === "library"
              ? "素材分析"
              : view === "devices"
                ? "设备导入"
                : view === "history"
                  ? "导出记录"
                  : view === "diagnostics"
                    ? "诊断日志"
                    : "使用说明"}
          </span>
          <div className="topbar-actions">
            {demo ? (
              <>
                <Badge variant="outline">演示数据</Badge>
                <Button variant="ghost" size="sm" onClick={leaveDemo}>
                  退出演示
                </Button>
              </>
            ) : (
              <span className="local-tag">
                <ShieldCheck size={14} />
                仅在本机处理
              </span>
            )}
          </div>
        </header>
        <div className="workspace">
          <div className="page-heading">
            <div>
              <div className="eyebrow">YOUR MOMENTS, IN ORDER</div>
              <h1>
                {view === "library"
                  ? "从一张存储卡，到井然有序。"
                  : view === "devices"
                    ? "从设备到资料库，每一帧都妥善保存。"
                    : view === "history"
                      ? "每一次归档，都有迹可循。"
                      : view === "diagnostics"
                        ? "离线留痕，问题有据可查。"
                        : "开始整理你的影像。"}
              </h1>
              <p>
                {view === "library"
                  ? "分析你的相机素材，找到需要的片段，轻松归档。"
                  : view === "devices"
                    ? "连接摄影设备，挑选原始素材，按你的拍摄习惯归档。"
                    : view === "history"
                      ? "查看本次运行中的导出结果与失败明细。"
                      : view === "diagnostics"
                        ? "连接过程与运行异常保存在本机，随时查看，事后导出排查。"
                        : "选择、筛选、导出。三步完成照片与视频整理。"}
              </p>
            </div>
            <span className="heading-icon">
              <Aperture size={38} />
            </span>
          </div>
          {error && (
            <div className="message error" role="alert">
              <TriangleAlert size={18} />
              <span>{error}</span>
              <Button
                size="icon"
                variant="ghost"
                aria-label="关闭错误提示"
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
              <Button
                size="icon"
                variant="ghost"
                aria-label="关闭状态提示"
                onClick={() => setNotice("")}
              >
                <X size={16} />
              </Button>
            </div>
          )}
          {monitorError && job.kind === "scan" && (
            <div className="message error" role="alert">
              <TriangleAlert size={18} />
              <span>{monitorError}</span>
              <Button
                variant="outline"
                size="sm"
                onClick={() => setMonitorRevision((n) => n + 1)}
              >
                重新获取进度
              </Button>
            </div>
          )}
          <div
            className="sr-only"
            aria-live={view === "library" ? "polite" : "off"}
          >
            {loading
              ? "正在加载素材列表"
              : `找到 ${result.total} 个素材，已选择 ${selected.size} 个`}
          </div>

          <div hidden={view !== "diagnostics"}>
            <DiagnosticsPage active={view === "diagnostics"} demo={demo} />
          </div>
          <div hidden={view !== "devices"}>
            <DeviceImportPage
              demo={demo}
              onPending={setDevicePending}
              onLocal={() => setView("library")}
              onAnalyze={(path) => {
                setView("library");
                setSource(path);
                void run(async () => {
                  if (busy)
                    throw new Error(
                      "请等待当前本地扫描或导出完成后再分析导入目录",
                    );
                  setJob(await api.scan(path));
                });
              }}
            />
          </div>
          {view === "library" ? (
            <>
              <section className="source-panel" aria-labelledby="source-title">
                <div className="source-icon">
                  <HardDrive size={23} />
                </div>
                <div className="source-info">
                  <div className="section-kicker" id="source-title">
                    01 / 分析来源{" "}
                    {loaded && source === summary.path && (
                      <span className="source-ready">
                        <span className="status-dot" />
                        已分析
                      </span>
                    )}
                  </div>
                  <div className="source-field">
                    <Input
                      aria-label="分析文件夹路径"
                      value={source}
                      onChange={(e) => setSource(e.target.value)}
                      placeholder="选择存储卡、机内存储挂载目录或本地文件夹"
                      disabled={busy || demo}
                    />
                    {source && (
                      <Button
                        aria-label="重新分析当前路径"
                        title="重新分析当前路径"
                        size="icon"
                        variant="ghost"
                        onClick={scan}
                        disabled={busy || demo}
                      >
                        <RotateCcw size={16} />
                      </Button>
                    )}
                  </div>
                  <p>
                    递归分析子文件夹 · 支持 RAW、JPEG、HEIC、MP4、MOV 等格式
                  </p>
                </div>
                <Button
                  variant="outline"
                  onClick={chooseSource}
                  disabled={busy || demo}
                >
                  <FolderOpen size={16} />
                  {loaded ? "更换文件夹" : "选择分析文件夹"}
                </Button>
              </section>

              {job.state === "running" && job.kind === "scan" && (
                <section className="job-panel" aria-label="任务进度">
                  <LoaderCircle className="spin" size={20} />
                  <div>
                    <strong>正在分析素材</strong>
                    <p>{job.current || "准备文件…"}</p>
                    <div className="indeterminate" />
                  </div>
                  <span>{`已找到 ${number(job.completed)} 个`}</span>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() =>
                      run(async () => {
                        await api.cancel();
                      })
                    }
                  >
                    取消任务
                  </Button>
                </section>
              )}

              {!loaded ? (
                <section className="welcome-panel">
                  <div className="welcome-visual">
                    <div className="card-outline">
                      <Aperture size={48} />
                      <span>MEMORY / MOMENTS</span>
                      <div className="card-contacts">▰ ▰ ▰ ▰ ▰</div>
                    </div>
                    <span className="welcome-floating">
                      <FolderOpen size={18} />
                      <Check size={14} />
                    </span>
                  </div>
                  <div className="eyebrow">
                    A LITTLE ORDER. MORE ROOM TO CREATE.
                  </div>
                  <h2>你的下一次创作，从整理开始。</h2>
                  <p>
                    连接相机存储卡或挂载机内存储，选择一个文件夹，
                    <br />
                    查看照片与视频的时间分布，并将需要的素材整理到资料库。
                  </p>
                  <Button onClick={chooseSource} disabled={busy}>
                    <FolderOpen size={17} />
                    选择分析文件夹
                    <ArrowRight size={16} />
                  </Button>
                  <button
                    className="demo-link"
                    onClick={() => {
                      setError("");
                      setDemo(true);
                    }}
                  >
                    <Sparkles size={14} />
                    先体验演示数据
                  </button>
                  <div className="welcome-steps">
                    <span>
                      <b>01</b>分析来源
                    </span>
                    <ChevronRight size={14} />
                    <span>
                      <b>02</b>筛选素材
                    </span>
                    <ChevronRight size={14} />
                    <span>
                      <b>03</b>按日期归档
                    </span>
                  </div>
                </section>
              ) : (
                <>
                  <div className="stats-grid">
                    <section className="stat-card">
                      <div className="stat-label">
                        <span>素材总量</span>
                        <span className="stat-icon">
                          <Layers size={18} />
                        </span>
                      </div>
                      <div className="stat-value">
                        {number(summary.count)}
                        <small>个文件</small>
                      </div>
                      <div className="stat-foot">
                        占用空间 <strong>{bytes(summary.bytes)}</strong>
                      </div>
                    </section>
                    <section className="stat-card">
                      <div className="stat-label">
                        <span>照片 / 视频</span>
                        <div className="mini-legend">
                          <span className="photo-dot" />
                          照片
                          <span className="video-dot" />
                          视频
                        </div>
                      </div>
                      <div className="split-stat">
                        <div>
                          {number(summary.photos)}
                          <Camera size={17} />
                        </div>
                        <span>/</span>
                        <div>
                          {number(summary.videos)}
                          <Film size={17} />
                        </div>
                      </div>
                      <div className="ratio-track">
                        <span
                          style={{
                            width: `${summary.count ? (summary.photos / summary.count) * 100 : 0}%`,
                          }}
                        />
                        <span
                          style={{
                            width: `${summary.count ? (summary.videos / summary.count) * 100 : 0}%`,
                          }}
                        />
                      </div>
                    </section>
                    <section className="stat-card">
                      <div className="stat-label">
                        <span>拍摄日期分布</span>
                        <span className="stat-icon">
                          <CalendarDays size={18} />
                        </span>
                      </div>
                      <div className="stat-value">
                        {summary.days.length}
                        <small>个拍摄日</small>
                      </div>
                      <div className="stat-foot">
                        {summary.days.length
                          ? `${dateLabel(summary.days[0].date)} — ${dateLabel(summary.days.at(-1)!.date)}`
                          : "暂无素材"}
                      </div>
                    </section>
                  </div>

                  <div className="content-grid">
                    <div className="analysis-column">
                      <section className="timeline-panel">
                        <div className="panel-heading">
                          <div>
                            <h2>
                              时间分布
                              <span className="section-index">/ TIMELINE</span>
                            </h2>
                            <p>
                              点击日期快速筛选素材
                              {summary.days.length > 24
                                ? "，长区间按月汇总"
                                : ""}
                            </p>
                          </div>
                          <div className="mini-legend">
                            <span className="photo-dot" />
                            照片
                            <span className="video-dot" />
                            视频
                          </div>
                        </div>
                        <div
                          className="timeline-chart"
                          aria-label="素材时间分布"
                        >
                          {chartDays.length ? (
                            chartDays.map((d) => (
                              <button
                                key={d.date}
                                className={`chart-column ${filter.from === d.from && filter.to === d.to ? "chosen" : ""}`}
                                title={`${d.date}：${d.photos} 张照片，${d.videos} 个视频，${bytes(d.bytes)}`}
                                aria-label={`${d.date}，照片 ${d.photos}，视频 ${d.videos}，点击筛选`}
                                onClick={() =>
                                  changeFilter({ from: d.from, to: d.to })
                                }
                                disabled={busy}
                              >
                                <span className="chart-count">
                                  {d.photos + d.videos}
                                </span>
                                <span className="bar-space">
                                  <span
                                    className="stacked-bar"
                                    style={{
                                      height: `${Math.max(4, ((d.photos + d.videos) / maxDay) * 100)}%`,
                                    }}
                                  >
                                    <span
                                      className="bar-video"
                                      style={{ flex: d.videos }}
                                    />
                                    <span
                                      className="bar-photo"
                                      style={{ flex: d.photos }}
                                    />
                                  </span>
                                </span>
                                <span className="chart-date">{d.label}</span>
                              </button>
                            ))
                          ) : (
                            <div className="chart-empty">
                              该文件夹中没有支持的照片或视频
                            </div>
                          )}
                        </div>
                      </section>

                      <section
                        className="files-panel"
                        aria-labelledby="files-title"
                      >
                        <div className="panel-heading">
                          <div>
                            <h2 id="files-title">
                              素材列表
                              <Badge variant="secondary">
                                {number(result.total)}
                              </Badge>
                            </h2>
                            <p>
                              {filtered ? "当前筛选结果" : "全部照片与视频"} ·{" "}
                              {bytes(result.bytes)}
                            </p>
                          </div>
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={resetFilters}
                            disabled={!filtered}
                          >
                            <SlidersHorizontal size={14} />
                            重置筛选
                          </Button>
                        </div>
                        <div className="filter-row">
                          <div className="search-field">
                            <Search size={16} />
                            <Input
                              ref={searchRef}
                              aria-label="搜索文件名或路径"
                              placeholder="搜索文件名或路径…"
                              value={searchText}
                              onCompositionStart={() => setComposing(true)}
                              onCompositionEnd={() => setComposing(false)}
                              onChange={(e) => setSearchText(e.target.value)}
                            />
                            {searchText && (
                              <button
                                aria-label="清空搜索"
                                onClick={() => {
                                  setSearchText("");
                                  changeFilter({ search: "" });
                                  searchRef.current?.focus();
                                }}
                              >
                                <X size={14} />
                              </button>
                            )}
                          </div>
                          <MediaSelect
                            value={filter.kind}
                            onValueChange={(kind) => changeFilter({ kind })}
                          >
                            <SelectTrigger
                              aria-label="素材类型"
                              className="type-select"
                            >
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              <SelectItem value="all">全部类型</SelectItem>
                              <SelectItem value="photo">仅照片</SelectItem>
                              <SelectItem value="video">仅视频</SelectItem>
                            </SelectContent>
                          </MediaSelect>
                        </div>
                        <div className="date-row">
                          <CalendarDays size={15} />
                          <label htmlFor="date-from">日期区间</label>
                          <Input
                            id="date-from"
                            type="date"
                            aria-describedby={
                              rangeError ? "date-error" : undefined
                            }
                            aria-invalid={rangeError}
                            value={filter.from}
                            onChange={(e) =>
                              changeFilter({ from: e.target.value })
                            }
                          />
                          <span>至</span>
                          <Input
                            aria-label="结束日期"
                            type="date"
                            aria-describedby={
                              rangeError ? "date-error" : undefined
                            }
                            aria-invalid={rangeError}
                            value={filter.to}
                            onChange={(e) =>
                              changeFilter({ to: e.target.value })
                            }
                          />
                          {(filter.from || filter.to) && (
                            <Button
                              variant="ghost"
                              size="icon"
                              aria-label="清空日期区间"
                              onClick={() => changeFilter({ from: "", to: "" })}
                            >
                              <X size={14} />
                            </Button>
                          )}
                          <MediaSelect
                            value={filter.sort}
                            onValueChange={(sort) => changeFilter({ sort })}
                          >
                            <SelectTrigger
                              aria-label="排序方式"
                              className="sort-select"
                            >
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              <SelectItem value="date">日期从新到旧</SelectItem>
                              <SelectItem value="size">大小从大到小</SelectItem>
                              <SelectItem value="name">文件名排序</SelectItem>
                            </SelectContent>
                          </MediaSelect>
                        </div>
                        {rangeError && (
                          <p id="date-error" className="field-error">
                            开始日期不能晚于结束日期，请调整日期区间。
                          </p>
                        )}
                        <div className="selection-strip">
                          <span>
                            已选择 <b>{number(selected.size)}</b> 个
                            {plan && ` · ${bytes(plan.bytes)}`}
                          </span>
                          <div>
                            <Button
                              variant="ghost"
                              size="sm"
                              disabled={!result.total || busy}
                              onClick={selectAll}
                            >
                              选择全部筛选结果
                            </Button>
                            <Button
                              variant="ghost"
                              size="sm"
                              disabled={!selected.size || busy}
                              onClick={() => setSelected(new Set())}
                            >
                              清空选择
                            </Button>
                          </div>
                        </div>
                        <div className="table-scroll" aria-busy={loading}>
                          <table>
                            <thead>
                              <tr>
                                <th className="check-cell">
                                  <Checkbox
                                    aria-label="选择当前页全部素材"
                                    checked={
                                      pageSelected
                                        ? true
                                        : pagePartial
                                          ? "indeterminate"
                                          : false
                                    }
                                    onCheckedChange={togglePage}
                                    disabled={busy}
                                  />
                                </th>
                                <th>文件名称</th>
                                <th>拍摄时间</th>
                                <th className="size-cell">大小</th>
                              </tr>
                            </thead>
                            <tbody>
                              {result.files.map((f) => (
                                <tr
                                  key={f.id}
                                  data-selected={selected.has(f.id)}
                                >
                                  <td className="check-cell">
                                    <Checkbox
                                      aria-label={`选择 ${f.name}`}
                                      checked={selected.has(f.id)}
                                      onCheckedChange={() => toggle(f.id)}
                                      disabled={busy}
                                    />
                                  </td>
                                  <td>
                                    <div className="file-cell">
                                      <MediaThumbnail
                                        file={f}
                                        scanPath={summary.path}
                                        demo={demo}
                                      />
                                      <button
                                        className="file-name"
                                        onClick={() => setDetail(f)}
                                      >
                                        <strong>{f.name}</strong>
                                        <span title={f.relativePath}>
                                          {f.relativePath}
                                        </span>
                                      </button>
                                    </div>
                                  </td>
                                  <td>
                                    <span className="file-date">
                                      {dateLabel(f.date.slice(0, 10))}
                                      <small>
                                        {f.date.slice(11, 16)}{" "}
                                        <span
                                          className={
                                            f.dateSource === "文件修改时间"
                                              ? "fallback"
                                              : ""
                                          }
                                        >
                                          {f.dateSource === "文件修改时间"
                                            ? "修改时间"
                                            : f.kind === "photo"
                                              ? "EXIF"
                                              : "视频元数据"}
                                        </span>
                                      </small>
                                    </span>
                                  </td>
                                  <td className="size-cell">{bytes(f.size)}</td>
                                </tr>
                              ))}
                            </tbody>
                          </table>
                          {!result.files.length && (
                            <div className="table-empty">
                              <Search size={27} />
                              <strong>
                                {summary.count
                                  ? "没有符合条件的素材"
                                  : "没有找到照片或视频"}
                              </strong>
                              <p>
                                {summary.count
                                  ? "试着调整日期、类型或搜索关键词。"
                                  : "请选择包含支持格式的文件夹，或检查文件夹权限。"}
                              </p>
                              {filtered && (
                                <Button
                                  variant="outline"
                                  size="sm"
                                  onClick={resetFilters}
                                >
                                  重置筛选
                                </Button>
                              )}
                            </div>
                          )}
                        </div>
                        <div className="pagination">
                          <span>
                            {result.total
                              ? `${(filter.page - 1) * 30 + 1}–${Math.min(filter.page * 30, result.total)} / ${number(result.total)} 个`
                              : "0 个素材"}
                          </span>
                          <div>
                            <Button
                              variant="ghost"
                              size="icon"
                              aria-label="上一页"
                              disabled={filter.page <= 1 || loading}
                              onClick={() =>
                                setFilter((q) => ({ ...q, page: q.page - 1 }))
                              }
                            >
                              <ChevronLeft size={16} />
                            </Button>
                            <span>
                              {filter.page} / {pages}
                            </span>
                            <Button
                              variant="ghost"
                              size="icon"
                              aria-label="下一页"
                              disabled={filter.page >= pages || loading}
                              onClick={() =>
                                setFilter((q) => ({ ...q, page: q.page + 1 }))
                              }
                            >
                              <ChevronRight size={16} />
                            </Button>
                          </div>
                        </div>
                      </section>
                    </div>
                    <aside className="export-column">
                      <section className="export-panel">
                        <div className="export-heading">
                          <span className="section-kicker">
                            02 / 整理与导出
                          </span>
                          <span className="export-icon">
                            <ArrowDownToLine size={20} />
                          </span>
                        </div>
                        <h2>为影像找到新归属。</h2>
                        <p>选择素材后，自动按类型与日期整理。</p>
                        <label className="field-label" htmlFor="destination">
                          导出到
                        </label>
                        <Input
                          id="destination"
                          value={destination}
                          onChange={(e) => setDestination(e.target.value)}
                          placeholder="选择或输入导出文件夹"
                          disabled={busy || demo}
                        />
                        <Button
                          variant="outline"
                          className="full-width"
                          onClick={chooseDestination}
                          disabled={busy}
                        >
                          <FolderOpen size={16} />
                          {destination ? "更换导出文件夹" : "选择导出文件夹"}
                        </Button>
                        <div className="folder-preview">
                          <div>
                            <FolderOpen size={16} />
                            <strong>导出文件夹</strong>
                          </div>
                          <div className="folder-tree">
                            {plan?.directories.length ? (
                              plan.directories.slice(0, 6).map((d) => (
                                <div key={d.date}>
                                  <Folder size={14} />
                                  <span>{d.date}/</span>
                                  <small>{d.photos + d.videos}</small>
                                </div>
                              ))
                            ) : (
                              <>
                                <div>
                                  <Folder size={14} />
                                  <span>photo / 年 / 月 / 日</span>
                                </div>
                                <div>
                                  <Folder size={14} />
                                  <span>video / 年 / 月 / 日</span>
                                </div>
                              </>
                            )}
                            {plan && plan.directories.length > 6 && (
                              <p>
                                另有 {plan.directories.length - 6} 个日期目录
                              </p>
                            )}
                          </div>
                        </div>
                        <div className="export-total">
                          <span>本次导出</span>
                          <strong>
                            {number(selected.size)}
                            <small>个文件</small>
                          </strong>
                          <span>
                            预计占用 <b>{bytes(plan?.bytes || 0)}</b>
                          </span>
                        </div>
                        <Button
                          className="export-button full-width"
                          disabled={
                            !selected.size ||
                            !destination.trim() ||
                            busy ||
                            !plan
                          }
                          onClick={() => {
                            setError("");
                            setConfirm(true);
                          }}
                        >
                          <ArrowDownToLine size={17} />
                          预览并导出
                          <ArrowRight size={16} />
                        </Button>
                        <p className="export-hint">
                          {!selected.size
                            ? "请先在列表中选择需要整理的素材"
                            : !destination
                              ? "选择导出文件夹后即可继续"
                              : "复制完成后进行 SHA-256 内容校验"}
                        </p>
                        <div className="safe-note">
                          <ShieldCheck size={16} />
                          <span>保留原始文件，同名自动编号</span>
                        </div>
                      </section>
                      <div className="metadata-note">
                        <CalendarDays size={18} />
                        <div>
                          <strong>关于拍摄时间</strong>
                          <p>
                            优先读取照片 EXIF
                            与视频元数据。读取不到时使用文件修改时间。
                          </p>
                          <span>
                            {summary.fallback
                              ? `${number(summary.fallback)} 个文件使用修改时间`
                              : "所有素材均已读取拍摄时间"}
                          </span>
                        </div>
                      </div>
                      {summary.warnings.length > 0 && (
                        <div className="scan-warnings" role="status">
                          <TriangleAlert size={17} />
                          <strong>部分文件无法读取</strong>
                          {summary.warnings.slice(0, 4).map((w) => (
                            <p key={w}>{w}</p>
                          ))}
                        </div>
                      )}
                      {summary.ignored > 0 && (
                        <p className="ignored-note">
                          已跳过 {number(summary.ignored)}{" "}
                          个非媒体、隐藏或链接文件
                        </p>
                      )}
                    </aside>
                  </div>
                </>
              )}
            </>
          ) : view === "history" ? (
            <section className="records-panel">
              {history.length ? (
                history.map((j) => (
                  <article className="record" key={j.id}>
                    <span className="record-icon">
                      <FolderOpen size={22} />
                    </span>
                    <div>
                      <h2>
                        {j.state === "cancelled"
                          ? "已取消"
                          : j.state === "failed"
                            ? "导出失败"
                            : j.failed
                              ? "部分完成"
                              : "导出完成"}
                        <Badge variant="outline">
                          {j.copied} 个成功 · {j.failed} 个失败
                        </Badge>
                      </h2>
                      <p>{j.destination}</p>
                      <span>
                        已处理 {j.completed} / {j.total} · {bytes(j.bytes)}
                      </span>
                      {j.errors.length > 0 && (
                        <details>
                          <summary>查看失败明细（{j.errors.length}）</summary>
                          {j.errors.map((e, i) => (
                            <p key={i}>{e}</p>
                          ))}
                        </details>
                      )}
                    </div>
                  </article>
                ))
              ) : (
                <div className="records-empty">
                  <History size={36} />
                  <h2>还没有导出记录</h2>
                  <p>
                    完成一次导出后，结果和失败明细会显示在这里。记录仅保留在本次运行中。
                  </p>
                  <Button variant="outline" onClick={() => setView("library")}>
                    返回素材分析
                  </Button>
                </div>
              )}
            </section>
          ) : view === "help" ? (
            <section className="help-panel">
              {[
                {
                  title: "连接设备或分析本地素材",
                  text: "Action 6 可通过“设备导入”完成蓝牙配对、Wi-Fi 连接和原片导入。其他设备可将存储卡插入读卡器，或将相机机内存储挂载为本地磁盘。点击“选择分析文件夹”，也可输入路径后点击重新分析。支持递归扫描子文件夹。相机仅提供 MTP/PTP 时，需要先使用系统工具复制到本地。",
                  icon: HardDrive,
                },
                {
                  title: "按时间与文件筛选",
                  text: "点击时间分布图快速选择某日，或设置开始与结束日期（包含边界日期）。可按类型、文件名或路径筛选。勾选当前页，或选择全部筛选结果。切换筛选时保留已有选择，清空选择会移除全部已选文件。",
                  icon: CalendarDays,
                },
                {
                  title: "预览并导出",
                  text: "选择独立的导出文件夹，预览目录后开始复制。照片放入 photo/年/月/日，视频放入 video/年/月/日。月份和日期使用两位数字。保留文件名，同名时追加 _001 等序号。复制完成后逐文件校验 SHA-256。",
                  icon: ArrowDownToLine,
                },
              ].map((step, i) => (
                <article key={step.title}>
                  <span className="help-number">0{i + 1}</span>
                  <step.icon size={27} />
                  <div>
                    <h2>{step.title}</h2>
                    <p>{step.text}</p>
                  </div>
                </article>
              ))}
              <div className="help-details">
                <h2>格式与时间说明</h2>
                <p>
                  支持常见照片、RAW 和视频扩展名。内置 EXIF 支持 JPEG/TIFF
                  和部分 TIFF 系 RAW；HEIC、CR3
                  等未能解析拍摄时间的格式会回退到修改时间。MP4/MOV 读取 mvhd
                  创建时间，并转换到本机时区；相机时间设置错误时，结果也会受影响。
                </p>
                <p>
                  取消导出后保留已完成文件，移除正在复制的不完整文件。意外断电或强制退出可能留下不完整文件，请根据导出记录检查。重复导出会自动编号，不会自动去重。扫描期间保持存储设备连接。
                </p>
              </div>
            </section>
          ) : null}
          <footer className="workspace-footer">
            <span>
              CardAtlas<span> / </span>让记忆有序，让创作继续。
            </span>
            <span>Go + Wails V3 · React 19</span>
          </footer>
        </div>
      </main>

      <Dialog open={confirm} onOpenChange={setConfirm}>
        <DialogContent className="confirm-dialog">
          <DialogHeader>
            <DialogTitle>确认整理与导出</DialogTitle>
            <DialogDescription>
              {demo
                ? "演示模式仅展示归档规则，不会复制真实文件。"
                : "点击“开始复制”后，将显示实时复制与校验进度。原始文件保留不变。"}
            </DialogDescription>
          </DialogHeader>
          <div className="confirm-destination">
            <FolderOpen size={19} />
            <span>{destination}</span>
          </div>
          <div className="confirm-summary">
            <strong>{number(selected.size)} 个文件</strong>
            <span>
              {bytes(plan?.bytes || 0)} · {plan?.directories.length || 0}{" "}
              个日期目录
            </span>
          </div>
          <div className="confirm-paths">
            {plan?.examples.map((p) => (
              <div key={p}>
                <FileImage size={15} />
                <code>{p}</code>
              </div>
            ))}
          </div>
          <p className="confirm-note">
            <ShieldCheck size={16} />
            同名文件自动编号，每个文件复制后校验内容。
          </p>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => setConfirm(false)}
              disabled={acting}
            >
              返回调整
            </Button>
            <Button onClick={exportFiles} disabled={acting}>
              {acting ? (
                <LoaderCircle className="spin" size={16} />
              ) : (
                <ArrowDownToLine size={16} />
              )}{" "}
              {demo ? "完成演示预览" : "开始复制"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      {job.kind === "export" && (
        <ExportTaskDialog
          job={job}
          open={exportOpen}
          onOpenChange={setExportOpen}
          onCancel={api.cancel}
          onHistory={() => {
            setExportOpen(false);
            showHistory();
          }}
          monitorError={monitorError}
          onRetry={() => setMonitorRevision((n) => n + 1)}
        />
      )}
      {job.kind === "export" && !exportOpen && (
        <button
          className="export-task-indicator"
          onClick={() => setExportOpen(true)}
        >
          {job.state === "running" || job.state === "starting" ? (
            <LoaderCircle className="spin" size={17} />
          ) : job.failed || job.state === "failed" ? (
            <TriangleAlert size={17} />
          ) : (
            <CheckCheck size={17} />
          )}
          <span>
            {exportTitle(job)}
            {job.state === "running" && ` · ${exportProgress(job).toFixed(1)}%`}
          </span>
          <ArrowRight size={16} />
        </button>
      )}
      <Dialog
        open={Boolean(detail)}
        onOpenChange={(open) => {
          if (!open) setDetail(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{detail?.name}</DialogTitle>
            <DialogDescription>素材信息与归档位置</DialogDescription>
          </DialogHeader>
          {detail && (
            <dl className="detail-list">
              <dt>原始路径</dt>
              <dd>{detail.relativePath}</dd>
              <dt>文件类型</dt>
              <dd>
                {detail.kind === "photo" ? "照片" : "视频"} · {detail.extension}
              </dd>
              <dt>文件大小</dt>
              <dd>{bytes(detail.size)}</dd>
              <dt>拍摄时间</dt>
              <dd>{detail.date.replace("T", " ")}（本机时间）</dd>
              <dt>时间来源</dt>
              <dd>{detail.dateSource}</dd>
              <dt>整理后位置</dt>
              <dd>{`${detail.kind}/${detail.date.slice(0, 10).replaceAll("-", "/")}/${detail.name}`}</dd>
            </dl>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => setDetail(null)}>
              关闭
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

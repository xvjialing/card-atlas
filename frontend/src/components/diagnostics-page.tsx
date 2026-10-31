import { useEffect, useRef, useState } from "react";
import {
  Archive,
  Check,
  FileText,
  FolderOpen,
  HardDrive,
  LoaderCircle,
  RefreshCw,
  Search,
  ShieldCheck,
  TriangleAlert,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select as LogSelect,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { isDesktop } from "@/lib/api";
import { bytes, number } from "@/lib/types";
import {
  diagnosticsAPI,
  logComponentLabel,
  logLevelLabel,
  logTime,
  type LogEntry,
  type LogInfo,
  type LogPage,
} from "@/lib/diagnostics";

const emptyPage: LogPage = {
  entries: [],
  nextCursor: "",
  hasMore: false,
  skipped: 0,
};
const messageOf = (error: unknown) =>
  error instanceof Error ? error.message : String(error);

export function DiagnosticsPage({
  active,
  demo,
}: {
  active: boolean;
  demo: boolean;
}) {
  const [info, setInfo] = useState<LogInfo | null>(null);
  const [page, setPage] = useState<LogPage>(emptyPage);
  const [text, setText] = useState("");
  const [search, setSearch] = useState("");
  const [composing, setComposing] = useState(false);
  const [level, setLevel] = useState("all");
  const [revision, setRevision] = useState(0);
  const [loading, setLoading] = useState(false);
  const [moreLoading, setMoreLoading] = useState(false);
  const [acting, setActing] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [exportPath, setExportPath] = useState("");
  const input = useRef<HTMLInputElement>(null);
  const generation = useRef(0);
  const operation = useRef(false);
  const morePending = useRef(false);
  const available = isDesktop && !demo;

  useEffect(() => {
    if (composing) return;
    const timer = setTimeout(() => setSearch(text.trim()), text ? 300 : 0);
    return () => clearTimeout(timer);
  }, [text, composing]);

  useEffect(() => {
    const current = ++generation.current;
    if (!active || !available) return;
    setLoading(true);
    setMoreLoading(false);
    morePending.current = false;
    setError("");
    Promise.all([
      diagnosticsAPI.info(),
      diagnosticsAPI.read({ level, search, before: "", limit: 50 }),
    ])
      .then(([nextInfo, nextPage]) => {
        if (current !== generation.current) return;
        setInfo(nextInfo);
        setPage(nextPage);
      })
      .catch((e) => {
        if (current === generation.current) setError(messageOf(e));
      })
      .finally(() => {
        if (current === generation.current) setLoading(false);
      });
    return () => {
      generation.current++;
    };
  }, [active, available, level, search, revision]);

  const clearSearch = () => {
    setText("");
    setSearch("");
    input.current?.focus();
  };
  const loadMore = async () => {
    if (morePending.current || loading || !page.hasMore) return;
    morePending.current = true;
    setMoreLoading(true);
    setError("");
    const current = generation.current;
    try {
      const next = await diagnosticsAPI.read({
        level,
        search,
        before: page.nextCursor,
        limit: 50,
      });
      if (current === generation.current)
        setPage((previous) => ({
          ...next,
          entries: [
            ...previous.entries,
            ...next.entries.filter(
              (entry) => !previous.entries.some((old) => old.id === entry.id),
            ),
          ],
        }));
    } catch (e) {
      if (current === generation.current) setError(messageOf(e));
    } finally {
      if (current === generation.current) {
        setMoreLoading(false);
        morePending.current = false;
      }
    }
  };
  const run = async (name: string, work: () => Promise<void>) => {
    if (operation.current) return;
    operation.current = true;
    setActing(name);
    setError("");
    setNotice("");
    try {
      await work();
    } catch (e) {
      setError(messageOf(e));
    } finally {
      setActing("");
      operation.current = false;
    }
  };

  return (
    <section className="diagnostics-page" aria-label="诊断日志">
      <div className="diagnostic-summary">
        <span
          className={`diagnostic-status-icon ${info?.lastError ? "error" : ""}`}
        >
          <FileText size={25} />
        </span>
        <div className="diagnostic-recording">
          <strong>
            {!available
              ? "日志保存在桌面客户端"
              : info?.lastError
                ? "日志记录需要检查"
                : info
                  ? "本地自动记录已开启"
                  : "正在读取日志状态"}
          </strong>
          <p>断网也能记录，重启后可继续查看。</p>
        </div>
        <div className="diagnostic-storage">
          <HardDrive size={16} />
          <span>
            {info ? `${bytes(info.bytes)} / ${bytes(info.maxBytes)}` : "—"}
            <small>
              {info
                ? `${number(info.files)} 个日志文件 · 保留 ${info.settings.retentionDays} 天`
                : "自动滚动保存"}
            </small>
          </span>
        </div>
        <Button
          disabled={!available || Boolean(acting)}
          onClick={() =>
            void run("export", async () => {
              const path = await diagnosticsAPI.export();
              setExportPath(path);
              setNotice(
                path
                  ? "诊断包已保存，可在之后排查或分享。"
                  : "已取消导出，日志仍保存在本机。",
              );
            })
          }
        >
          {acting === "export" ? (
            <LoaderCircle className="spin" size={16} />
          ) : (
            <Archive size={16} />
          )}
          导出诊断包
        </Button>
      </div>

      <div className="diagnostic-feedback" aria-live="polite">
        {error && (
          <div className="message error" role="alert">
            <TriangleAlert size={18} />
            <span>{error}</span>
            <Button
              variant="outline"
              size="sm"
              disabled={loading || Boolean(acting)}
              onClick={() => setRevision((n) => n + 1)}
            >
              重新读取
            </Button>
          </div>
        )}
        {info?.lastError && (
          <div className="message error" role="alert">
            <TriangleAlert size={18} />
            <span>{info.lastError}。请检查磁盘空间或日志目录权限。</span>
          </div>
        )}
        {notice && (
          <div className="message" role="status">
            <Check size={18} />
            <span>
              {notice}
              {exportPath && (
                <code className="diagnostic-export-path">{exportPath}</code>
              )}
            </span>
          </div>
        )}
      </div>

      <div className="diagnostic-controls">
        <label className="diagnostic-search">
          <span className="sr-only">搜索诊断日志</span>
          <Search size={16} />
          <Input
            ref={input}
            value={text}
            maxLength={256}
            placeholder="搜索错误、连接阶段或事件"
            disabled={!available}
            onChange={(e) => setText(e.target.value)}
            onCompositionStart={() => setComposing(true)}
            onCompositionEnd={() => setComposing(false)}
          />
          {text && (
            <Button
              variant="ghost"
              size="icon"
              aria-label="清空日志搜索"
              onClick={clearSearch}
            >
              <X size={15} />
            </Button>
          )}
        </label>
        <div className="diagnostic-select">
          <label id="log-filter-label">筛选级别</label>
          <LogSelect
            value={level}
            onValueChange={setLevel}
            disabled={!available}
          >
            <SelectTrigger aria-labelledby="log-filter-label">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {["all", "error", "warn", "info", "debug"].map((value) => (
                <SelectItem key={value} value={value}>
                  {value === "all" ? "全部级别" : logLevelLabel(value)}
                </SelectItem>
              ))}
            </SelectContent>
          </LogSelect>
        </div>
        <Button
          variant="outline"
          aria-label="刷新日志"
          disabled={!available || loading || Boolean(acting)}
          onClick={() => setRevision((n) => n + 1)}
        >
          <RefreshCw size={16} className={loading ? "spin" : ""} />
          刷新
        </Button>
      </div>

      <div className="diagnostic-log-panel">
        <div className="diagnostic-list-heading">
          <h2>运行记录</h2>
          <span>
            {loading ? "正在读取…" : `已显示 ${number(page.entries.length)} 条`}{" "}
            · 最新在前 · 本机时间
          </span>
        </div>
        <div className="diagnostic-scroll" aria-busy={loading}>
          {!available ? (
            <div className="diagnostic-empty">
              <FileText size={32} />
              <h3>{demo ? "演示模式不读取真实日志" : "请使用桌面客户端"}</h3>
              <p>在 CardAtlas 中打开“诊断日志”，即可查看和导出本机记录。</p>
            </div>
          ) : !info && loading ? (
            <div className="diagnostic-empty" role="status">
              <LoaderCircle className="spin" size={28} />
              <p>正在读取本机日志…</p>
            </div>
          ) : page.entries.length ? (
            <ol className="diagnostic-entries">
              {page.entries.map((entry) => (
                <LogRow
                  key={entry.id}
                  entry={entry}
                  current={entry.session === info?.session}
                />
              ))}
            </ol>
          ) : (
            <div className="diagnostic-empty">
              <Search size={30} />
              <h3>
                {error
                  ? "暂时无法读取日志"
                  : search || level !== "all"
                    ? "没有匹配的记录"
                    : "还没有运行记录"}
              </h3>
              <p>
                {error
                  ? "记录没有被清除，可以重新读取。"
                  : search || level !== "all"
                    ? "试试其他关键词，或恢复全部级别。"
                    : "操作设备或开始素材任务后，记录会自动保存在本机。"}
              </p>
              {(search || level !== "all") && (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    clearSearch();
                    setLevel("all");
                  }}
                >
                  清除筛选
                </Button>
              )}
            </div>
          )}
        </div>
        <div className="diagnostic-list-footer">
          <span>
            {page.skipped > 0
              ? `已跳过 ${page.skipped} 条损坏记录，其他日志仍可查看。`
              : "刷新查看新记录，筛选不会影响日志保存。"}
          </span>
          {page.hasMore && (
            <Button
              variant="outline"
              size="sm"
              disabled={loading || moreLoading}
              onClick={() => void loadMore()}
            >
              {moreLoading && <LoaderCircle className="spin" size={14} />}
              加载更早记录
            </Button>
          )}
        </div>
      </div>

      <div className="diagnostic-settings">
        <div>
          <label id="log-record-level">记录详细程度</label>
          <p>排查连接时选择详细记录，之后可恢复标准记录。</p>
        </div>
        <LogSelect
          value={info?.settings.level ?? "info"}
          disabled={!available || !info || Boolean(acting)}
          onValueChange={(next) =>
            void run("level", async () => {
              const updated = await diagnosticsAPI.level(next);
              setInfo(updated);
              setNotice(
                next === "debug"
                  ? "已开启详细记录，后续操作会记录更多连接过程。"
                  : "已恢复标准记录，已有详细日志仍会保留。",
              );
              setExportPath("");
              setRevision((n) => n + 1);
            })
          }
        >
          <SelectTrigger aria-labelledby="log-record-level">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="info">标准记录</SelectItem>
            <SelectItem value="debug">详细记录</SelectItem>
          </SelectContent>
        </LogSelect>
        <Button
          variant="outline"
          disabled={!available || Boolean(acting)}
          onClick={() =>
            void run("directory", async () => {
              await diagnosticsAPI.openDirectory();
              setNotice("已打开日志文件夹。");
              setExportPath("");
            })
          }
        >
          <FolderOpen size={16} />
          打开日志文件夹
        </Button>
      </div>
      <div className="diagnostic-note">
        <ShieldCheck size={17} />
        <p>
          诊断包包含保留期内的运行日志与版本信息，不受当前筛选限制。密码、配对标识、已知热点名和本地路径会脱敏，不包含照片或视频。日志仅保存在本机。
        </p>
      </div>
    </section>
  );
}

function LogRow({ entry, current }: { entry: LogEntry; current: boolean }) {
  return (
    <li className="diagnostic-entry">
      <div className="diagnostic-entry-time">
        <time dateTime={entry.time} title={entry.time}>
          {logTime(entry.time)}
        </time>
        <span className={`diagnostic-level ${entry.level}`}>
          {logLevelLabel(entry.level)}
        </span>
      </div>
      <div className="diagnostic-entry-main">
        <div className="diagnostic-entry-meta">
          <span>{logComponentLabel(entry.component)}</span>
          {current && <span className="diagnostic-current">本次运行</span>}
        </div>
        <p>{entry.message}</p>
        <details>
          <summary>
            查看详情<span>{entry.event}</span>
          </summary>
          <dl>
            <dt>完整时间</dt>
            <dd>{entry.time}</dd>
            <dt>会话编号</dt>
            <dd>{entry.session}</dd>
            <dt>事件</dt>
            <dd>{entry.event}</dd>
            {Object.entries(entry.fields ?? {}).map(([key, value]) => (
              <div className="diagnostic-field" key={key}>
                <dt>{key}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
        </details>
      </div>
    </li>
  );
}

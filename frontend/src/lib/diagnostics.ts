import { isDesktop } from "./api";

export interface LogEntry {
  id: string;
  time: string;
  session: string;
  level: string;
  component: string;
  event: string;
  message: string;
  fields?: Record<string, string | undefined> | null;
}
export interface LogInfo {
  settings: { retentionDays: number; level: string };
  directory: string;
  files: number;
  bytes: number;
  maxBytes: number;
  session: string;
  lastError: string;
}
export interface LogPage {
  entries: LogEntry[];
  nextCursor: string;
  hasMore: boolean;
  skipped: number;
}
export interface LogQuery {
  level: string;
  search: string;
  before: string;
  limit: number;
}

const service = async () => {
  if (!isDesktop)
    throw new Error("请在 CardAtlas 桌面客户端中查看本地诊断日志。");
  return import("../../bindings/cardatlas/diagnosticsservice");
};
export const diagnosticsAPI = {
  info: async (): Promise<LogInfo> => (await service()).GetInfo(),
  read: async (query: LogQuery): Promise<LogPage> => {
    const page = await (await service()).GetLogs(query);
    return { ...page, entries: page.entries ?? [] };
  },
  level: async (level: string): Promise<LogInfo> =>
    (await service()).SetLevel(level),
  export: async (): Promise<string> => (await service()).ExportLogs(),
  openDirectory: async () => (await service()).OpenLogDirectory(),
};

// 不序列化错误对象、表单或页面内容；记录失败不能触发新的未处理异常。
export function installClientLogging() {
  if (!isDesktop) return;
  const report = (kind: string, value: unknown) => {
    const message =
      value instanceof Error
        ? `${value.name}: ${value.message}`
        : typeof value === "string"
          ? value
          : "界面抛出了非文字异常";
    void service()
      .then((api) => api.ReportClientError(kind, message.slice(0, 2000)))
      .catch(() => {});
  };
  window.addEventListener("error", (event) =>
    report("error", event.error ?? event.message),
  );
  window.addEventListener("unhandledrejection", (event) =>
    report("unhandledrejection", event.reason),
  );
}

export const logLevelLabel = (level: string) =>
  ({ info: "信息", warn: "警告", error: "错误", debug: "详细" })[level] ??
  level;
export const logComponentLabel = (component: string) =>
  ({
    app: "客户端",
    frontend: "界面",
    framework: "运行框架",
    bluetooth: "蓝牙",
    wifi: "Wi-Fi",
    datalink: "素材会话",
    connection: "设备连接",
    media: "素材浏览",
    transfer: "文件传输",
    scan: "素材扫描",
    export: "本地导出",
  })[component] ?? component;
export function logTime(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? value
    : date.toLocaleString("zh-CN", {
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
        hour12: false,
      });
}

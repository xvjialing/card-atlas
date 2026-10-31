// 仅由浏览器测试拦截模块加载，不打包进客户端。
const fixture = (window.__diagnosticsFixture = {
  calls: [],
  failRead: false,
  failExport: false,
  cancelExport: false,
  delayExport: 0,
  info: {
    settings: { retentionDays: 7, level: "info" },
    directory: "/测试/CardAtlas/devices/logs",
    files: 3,
    bytes: 18432,
    maxBytes: 104857600,
    session: "fixture-current",
    lastError: "",
  },
});
const entries = Array.from({ length: 65 }, (_, i) => ({
  id: `entry-${i}`,
  time: new Date(Date.UTC(2026, 9, 26, 14, 50, i)).toISOString(),
  session: i < 40 ? "fixture-current" : "fixture-previous",
  level: i % 4 === 0 ? "error" : i % 4 === 1 ? "warn" : "info",
  component: i % 2 ? "wifi" : "datalink",
  event: `test.connection.${i}`,
  message:
    i === 0
      ? "测试：新查询连接记录"
      : i === 1
        ? "测试：旧查询连接记录"
        : `测试：素材会话诊断事件 ${i}`,
  fields: {
    port: "9003",
    error:
      i === 0
        ? "测试：等待素材会话确认超时，Wi-Fi 握手已完成。".repeat(14)
        : "测试诊断信息已脱敏",
  },
}));
export const diagnosticsAPI = {
  info: async () => structuredClone(fixture.info),
  read: async (query) => {
    fixture.calls.push({ type: "read", ...query });
    if (fixture.failRead) {
      fixture.failRead = false;
      throw Error("测试：日志读取失败，请重试");
    }
    if (query.search === "旧查询")
      await new Promise((r) => setTimeout(r, 1100));
    let list = entries.filter(
      (e) =>
        (query.level === "all" || e.level === query.level) &&
        (!query.search || e.message.includes(query.search)),
    );
    const offset = Number(query.before || 0);
    return {
      entries: list.slice(offset, offset + query.limit),
      hasMore: list.length > offset + query.limit,
      nextCursor: String(offset + query.limit),
      skipped: 0,
    };
  },
  level: async (level) => {
    fixture.calls.push({ type: "level", level });
    fixture.info.settings.level = level;
    return structuredClone(fixture.info);
  },
  export: async () => {
    fixture.calls.push({ type: "export" });
    await new Promise((r) => setTimeout(r, fixture.delayExport));
    if (fixture.failExport) throw Error("测试：目标文件夹不可写");
    return fixture.cancelExport ? "" : "/测试/CardAtlas-诊断.zip";
  },
  openDirectory: async () => {
    fixture.calls.push({ type: "directory" });
  },
};
export const installClientLogging = () => {};
export const logLevelLabel = (level) =>
  ({ info: "信息", warn: "警告", error: "错误", debug: "详细" })[level] ??
  level;
export const logComponentLabel = (value) =>
  ({ wifi: "Wi-Fi", datalink: "素材会话" })[value] ?? value;
export const logTime = (value) =>
  new Date(value).toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });

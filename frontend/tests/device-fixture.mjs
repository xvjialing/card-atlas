// 仅由浏览器验收脚本拦截模块请求时注入，不打包进应用，不访问真实设备或本地原片。
export const emptyDeviceState = {
  providers: [
    {
      id: "dji-action6",
      name: "DJI Osmo Action 6",
      brand: "DJI",
      transport: "蓝牙配对 · Wi-Fi 传输",
      available: true,
      compatibility: "已接入，待实机验证",
      storages: [
        { id: "1", name: "机身存储" },
        { id: "0", name: "存储卡" },
      ],
    },
    {
      id: "dji-mini5pro",
      name: "DJI Mini 5 Pro",
      brand: "DJI",
      transport: "Wi-Fi 热点传输",
      available: true,
      compatibility: "已接入，待实机验证",
      storages: [{ id: "0", name: "存储卡" }],
    },
    {
      id: "sony-zve10",
      name: "Sony ZV-E10",
      brand: "Sony",
      transport: "Wi-Fi 热点传输",
      available: true,
      compatibility: "已接入，待实机验证",
      storages: [{ id: "0", name: "存储卡" }],
    },
  ],
  activeProvider: "dji-action6",
  tasks: [],
  warning: "自动化测试数据 · 未连接实机",
  connection: {
    sessionToken: "fixture-session",
    deviceId: "fixture-camera",
    deviceName: "",
    phase: "disconnected",
    message: "连接相机，开始导入原始素材",
    warning: "",
    ssid: "",
    busy: false,
    revision: 0,
    wifiNetworks: [],
  },
};
const media = Array.from({ length: 65 }, (_, i) => ({
  id: `file-${i}`,
  name: `DJI_${String(i).padStart(4, "0")}.${i % 2 ? "MP4" : "JPG"}`,
  path: `DCIM/100MEDIA/DJI_${String(i).padStart(4, "0")}.${i % 2 ? "MP4" : "JPG"}`,
  storageId: i % 3 ? "0" : "1",
  storageName: i % 3 ? "SD 卡" : "内置存储",
  size: 1024 * (i + 1),
  sizeEstimated: true,
  duration: i % 2 ? 75 : 0,
  kind: i % 2 ? "video" : "photo",
  date: `2026-10-${i % 2 ? "23" : "22"} 10:30:00`,
  dateSource: "设备清单（本机时区）",
  thumbnail: false,
}));
const clone = (x) => structuredClone(x);
const disconnected = () => ({
  sessionToken: "fixture-session-" + Math.random().toString(36).slice(2, 8),
  deviceId: "",
  deviceName: "",
  phase: "disconnected",
  message: "连接设备，开始导入原始素材",
  warning: "",
  ssid: "",
  busy: false,
  revision: 0,
  wifiNetworks: [],
});
const providerNetworks = (provider) => {
  if (provider === "sony-zve10")
    return [
      {
        ssid: "DIRECT-e6:ZV-E10",
        rssi: -44,
        channel: 6,
        security: "WPA2",
        supported: true,
        likely: true,
        reason: "与二维码照片读取的热点名称一致",
        hasSavedPassword: false,
        // 模拟二维码导入后已携带密码的状态。
        hasProvisionedPassword: true,
      },
    ];
  if (provider === "dji-mini5pro")
    return [
      {
        ssid: "DJI-Mini5Pro-测试",
        rssi: -46,
        channel: 149,
        security: "WPA2",
        supported: true,
        likely: true,
        reason: "名称包含设备特征，需由你确认",
        hasSavedPassword: false,
      },
    ];
  return [
    {
      ssid: "测试相机热点",
      rssi: -48,
      channel: 149,
      security: "WPA2",
      supported: true,
      likely: true,
      reason: "测试热点",
      hasSavedPassword: false,
    },
  ];
};
const providerName = (provider) =>
  ({
    "dji-action6": "DJI Osmo Action 6 测试设备",
    "dji-mini5pro": "DJI Mini 5 Pro",
    "sony-zve10": "Sony ZV-E10",
  })[provider] || "测试设备";
const fixture = (window.__deviceFixture = {
  state: clone(emptyDeviceState),
  media,
  calls: [],
  plan: null,
  failRefresh: false,
});
export const devicesAPI = {
  state: async () => clone(fixture.state),
  library: async () => clone(media),
  selectProvider: async (id) => {
    fixture.calls.push("select:" + id);
    fixture.state.activeProvider = id;
    fixture.state.connection = disconnected();
  },
  discover: async () => [
    {
      id: "fixture-camera",
      name: providerName(fixture.state.activeProvider),
      rssi: -48,
    },
  ],
  connect: async (r) => {
    fixture.calls.push(r.action);
    const provider = fixture.state.activeProvider;
    if (r.action === "prepare" || r.action === "scan_wifi") {
      Object.assign(fixture.state.connection, {
        phase: "choosing_wifi",
        deviceName: providerName(provider),
        message:
          provider === "sony-zve10" && fixture.calls.at(-2) === "import_qr"
            ? "已从二维码照片读取“DIRECT-e6:ZV-E10”的连接信息，请选择该热点并确认加入"
            : "请选择设备热点，确认后才会切换 Wi-Fi",
        wifiNetworks: providerNetworks(provider),
      });
    } else if (r.action === "import_qr") {
      Object.assign(fixture.state.connection, {
        phase: "choosing_wifi",
        deviceName: providerName(provider),
        message:
          "已从二维码照片读取“DIRECT-e6:ZV-E10”的连接信息，请选择该热点并确认加入",
        wifiNetworks: providerNetworks(provider),
      });
    } else if (r.action === "join_wifi" || r.action === "manual") {
      Object.assign(fixture.state.connection, {
        phase: "connected",
        revision: fixture.state.connection.revision + 1,
        ssid: fixture.state.connection.wifiNetworks[0]?.ssid || "测试相机热点",
        message: "已连接，可选择素材导入",
      });
    }
  },
  disconnect: async () => {
    fixture.state.connection.phase = "disconnected";
    fixture.state.tasks.forEach((t) => {
      if (["queued", "downloading"].includes(t.status)) t.status = "paused";
    });
  },
  refresh: async () => {
    if (fixture.failRefresh)
      throw Error("测试：SD 卡读取失败，已保留原素材清单");
    fixture.state.connection.revision++;
  },
  choose: async () => "/测试/影像资料库",
  prepare: async (ids, destination, layout) => {
    fixture.plan = {
      id: "fixture-plan",
      taskIds: ids.map((_, i) => `task-${i}`),
      destination,
      layout,
      deviceName: fixture.state.connection.deviceName,
      count: ids.length,
      bytes: ids.reduce((n, id) => n + media.find((f) => f.id === id).size, 0),
      skipped: 0,
      unknownDates: 0,
      examples: ids
        .slice(0, 5)
        .map((id) => `photo/2026/10/22/${media.find((f) => f.id === id).name}`),
      ids,
    };
    return clone(fixture.plan);
  },
  start: async () => {
    fixture.calls.push("start");
    fixture.state.tasks = [
      ...fixture.state.tasks,
      ...fixture.plan.ids.map((id, i) => ({
        id: `task-${i}`,
        providerId: "dji-action6",
        deviceId: "fixture-camera",
        deviceName: "DJI Osmo Action 6 测试设备",
        media: clone(media.find((f) => f.id === id)),
        root: fixture.plan.destination,
        relativePath: `photo/2026/10/22/${media.find((f) => f.id === id).name}`,
        layout: fixture.plan.layout,
        status: i ? "queued" : "downloading",
        progress: {
          bytes: i ? 0 : 500,
          total: media.find((f) => f.id === id).size,
          speed: i ? 0 : 1024,
          phase: "downloading",
        },
        error: "",
        warning: "",
        path: "",
        sha256: "",
        createdAt: Date.now(),
      })),
    ];
  },
  pause: async () => {
    fixture.state.tasks.forEach((t) => {
      if (["queued", "downloading"].includes(t.status)) {
        t.status = "paused";
        t.progress.speed = 0;
      }
    });
  },
  resume: async () => {
    const tasks = fixture.state.tasks.filter((t) =>
      ["paused", "failed"].includes(t.status),
    );
    tasks.forEach((t) => {
      t.status = "completed";
      t.path = "/测试/影像资料库/" + t.media.name;
      t.sha256 = "0".repeat(64);
      t.progress.bytes = t.progress.total;
    });
    return tasks.length;
  },
  retry: async (id) => {
    fixture.state.tasks.find((t) => t.id === id).status = "completed";
    fixture.state.tasks.find((t) => t.id === id).path =
      "/测试/影像资料库/已完成.JPG";
    fixture.state.tasks.find((t) => t.id === id).sha256 = "0".repeat(64);
    fixture.state.tasks.find((t) => t.id === id).progress.bytes =
      fixture.state.tasks.find((t) => t.id === id).progress.total;
  },
  reveal: async () => {
    fixture.calls.push("reveal");
  },
};
export const errorMessage = (e) => (e instanceof Error ? e.message : String(e));
export const deviceMediaURL = () => "/__fixture_media_unavailable__";
export const taskLabel = (t) =>
  ({
    queued: "等待导入",
    downloading: "正在导入",
    completed: "已导入",
    paused: "已暂停",
    failed: "导入失败",
  })[t.status] || t.status;

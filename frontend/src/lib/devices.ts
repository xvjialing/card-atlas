import { isDesktop } from "./api";
import type {
  Device,
  Media,
  State as WireState,
  Session as WireSession,
  Plan as WirePlan,
  Descriptor,
  Network,
  Task,
  ConnectionRequest,
} from "../../bindings/cardatlas/internal/deviceimport/models";
export type { Device, Media, Task };
export type Session = Omit<WireSession, "wifiNetworks"> & {
  wifiNetworks: Network[];
};
export type State = Omit<WireState, "providers" | "tasks" | "connection"> & {
  providers: Descriptor[];
  tasks: Task[];
  connection: Session;
};
export type Plan = Omit<WirePlan, "examples" | "taskIds"> & {
  examples: string[];
  taskIds: string[];
};
// Wails 的接口绑定允许 nil 切片；在应用边界一次性标准化。
function normalizeState(state: WireState): State {
  return {
    ...state,
    providers: state.providers ?? [],
    tasks: state.tasks ?? [],
    connection: {
      ...state.connection,
      wifiNetworks: state.connection.wifiNetworks ?? [],
    },
  };
}
export const emptyConnection: Session = {
  sessionToken: "",
  deviceId: "",
  deviceName: "",
  phase: "disconnected",
  message: "连接相机，开始导入原始素材",
  warning: "",
  ssid: "",
  busy: false,
  revision: 0,
  wifiNetworks: [],
};
export const emptyDeviceState: State = {
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
  ],
  activeProvider: "dji-action6",
  connection: emptyConnection,
  tasks: [],
  warning: "",
};
const service = async () => {
  if (!isDesktop)
    throw new Error("连接设备和导入原片需要使用 CardAtlas 桌面客户端。");
  return import("../../bindings/cardatlas/deviceservice");
};
export const devicesAPI = {
  state: async (): Promise<State> =>
    isDesktop
      ? normalizeState(await (await service()).GetState())
      : emptyDeviceState,
  library: async (): Promise<Media[]> =>
    (await (await service()).GetLibrary()) ?? [],
  discover: async (): Promise<Device[]> =>
    (await (await service()).Discover()) ?? [],
  connect: async (request: Partial<ConnectionRequest>) =>
    (await service()).Connect({
      action: "",
      deviceId: "",
      ssid: "",
      password: "",
      useSaved: false,
      ...request,
    }),
  disconnect: async () => (await service()).Disconnect(),
  refresh: async () => (await service()).Refresh(),
  choose: async () => (await service()).ChooseDestination(),
  prepare: async (
    ids: string[],
    destination: string,
    layout: string,
  ): Promise<Plan> => {
    const plan = await (
      await service()
    ).PrepareImport({ ids, destination, layout });
    return {
      ...plan,
      examples: plan.examples ?? [],
      taskIds: plan.taskIds ?? [],
    };
  },
  start: async (planID: string) => (await service()).StartImport(planID),
  pause: async () => (await service()).PauseImports(),
  resume: async () => (await service()).ResumeImports(),
  retry: async (id: string) => (await service()).RetryImport(id),
  reveal: async (id: string) => (await service()).RevealImport(id),
};
export function deviceMediaURL(
  provider: string,
  id: string,
  preview = false,
  session = "",
) {
  return `/device-media?${new URLSearchParams({ provider, id, session, preview: preview ? "1" : "0" })}`;
}
export const errorMessage = (error: unknown) =>
  error instanceof Error ? error.message : String(error);
export const taskLabel = (task: Task) =>
  task.status === "downloading" && task.progress.phase === "publishing"
    ? "正在保存并检查文件"
    : task.status === "downloading" && task.progress.phase === "fingerprinting"
      ? "正在生成本地指纹"
      : ({
          queued: "等待导入",
          downloading: "正在导入",
          completed: "已导入",
          paused: "已暂停",
          failed: "导入失败",
        }[task.status] ?? task.status);

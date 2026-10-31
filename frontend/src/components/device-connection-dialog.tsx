import { useEffect, useRef, useState, type RefObject } from "react";
import {
  Bluetooth,
  Camera,
  ChevronLeft,
  ChevronRight,
  Eye,
  EyeOff,
  KeyRound,
  LoaderCircle,
  QrCode,
  Radio,
  RefreshCw,
  ShieldCheck,
  Wifi,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  devicesAPI,
  errorMessage,
  type Device,
  type Session,
} from "@/lib/devices";
// “不再提醒”是纯界面偏好，存入 webview 本地存储；与路径、筛选等业务数据无关。
const WIFI_SWITCH_NOTICE_KEY = "cardatlas.wifiSwitchNotice.dismissed";

type Method =
  | "PrepareCamera"
  | "ScanWiFi"
  | "JoinCameraWiFi"
  | "ForgetWiFiPassword"
  | "ImportQR"
  | "Connect"
  | "Disconnect";
async function invoke(method: Method, args: unknown[]) {
  switch (method) {
    case "PrepareCamera":
      return devicesAPI.connect({
        action: "prepare",
        deviceId: String(args[0]),
      });
    case "ScanWiFi":
      return devicesAPI.connect({ action: "scan_wifi" });
    case "JoinCameraWiFi":
      return devicesAPI.connect({
        action: "join_wifi",
        ssid: String(args[0]),
        password: String(args[1]),
        useSaved: Boolean(args[2]),
      });
    case "ForgetWiFiPassword":
      return devicesAPI.connect({
        action: "forget_password",
        ssid: String(args[0]),
      });
    case "ImportQR":
      return devicesAPI.connect({
        action: "import_qr",
        photo: String(args[0]),
      });
    case "Connect":
      return devicesAPI.connect({ action: "manual" });
    case "Disconnect":
      return devicesAPI.disconnect();
  }
}
const deviceCopy: Record<
  string,
  {
    title: string;
    description: string;
    scan: { title: string; button: string; working: string; help: string };
  }
> = {
  "dji-action6": {
    title: "连接 DJI Osmo Action 6",
    description:
      "相机放在 Mac 附近，并退出手机 DJI Mimo。选定热点并确认后才会切换 Wi-Fi。",
    scan: {
      title: "搜索蓝牙相机",
      button: "搜索附近相机",
      working: "正在搜索…",
      help: "点击设备后先配对并扫描热点，首次配对请在相机屏幕上确认。",
    },
  },
  "dji-mini5pro": {
    title: "连接 DJI Mini 5 Pro",
    description:
      "首次授权与 DJI Fly 手机快传一致：配对时长按机身电源键约 3 秒。确认后才会切换 Wi-Fi。",
    scan: {
      title: "搜索蓝牙无人机",
      button: "搜索附近无人机",
      working: "正在搜索…",
      help: "点击设备后配对，并按提示长按机身电源键约 3 秒完成授权，听到提示音即成功。",
    },
  },
  "sony-zve10": {
    title: "连接 Sony ZV-E10",
    description:
      "在相机回放界面进入「发送到智能手机」并选择素材，拍下二维码后导入，或扫描相机热点。请先退出手机 Imaging Edge Mobile、Monitor+。确认后才会切换 Wi-Fi。",
    scan: {
      title: "搜索蓝牙相机",
      button: "搜索附近相机",
      working: "正在搜索…",
      help: "点击设备后先配对并扫描热点，首次配对请在相机屏幕上确认。",
    },
  },
};
// 把二维码照片缩放到常规尺寸，避免把整张原图提交给适配器。
async function readQRPhoto(file: File): Promise<string> {
  const url = URL.createObjectURL(file);
  try {
    const image = new Image();
    image.src = url;
    await image.decode();
    const scale = Math.min(
      1,
      1280 / Math.max(image.naturalWidth, image.naturalHeight),
    );
    const canvas = document.createElement("canvas");
    canvas.width = Math.max(1, Math.round(image.naturalWidth * scale));
    canvas.height = Math.max(1, Math.round(image.naturalHeight * scale));
    canvas
      .getContext("2d")
      ?.drawImage(image, 0, 0, canvas.width, canvas.height);
    return canvas.toDataURL("image/jpeg", 0.85);
  } finally {
    URL.revokeObjectURL(url);
  }
}

export function DeviceConnectionDialog({
  open,
  onOpenChange,
  state,
  onState,
  returnFocus,
  providerId,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  state: Session;
  onState: (state: Session) => void;
  returnFocus: RefObject<HTMLElement | null>;
  providerId: string;
}) {
  const [devices, setDevices] = useState<Device[]>([]);
  const [scanned, setScanned] = useState(false);
  const [working, setWorking] = useState("");
  const inFlight = useRef(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [selected, setSelected] = useState("");
  const [allNetworks, setAllNetworks] = useState(false);
  const [password, setPassword] = useState("");
  const [visible, setVisible] = useState(false);
  const [useSaved, setUseSaved] = useState(false);
  const [manualPassword, setManualPassword] = useState(false);
  const [fieldError, setFieldError] = useState("");
  const passwordInput = useRef<HTMLInputElement>(null);
  const photoInput = useRef<HTMLInputElement>(null);
  const [switchNotice, setSwitchNotice] = useState(false);
  const pendingJoin = useRef<null | (() => void)>(null);
  // ble：蓝牙配对 + 热点（Action 6）；aircraft：蓝牙电源键授权 + 热点（Mini 5 Pro）；wifi：纯热点（ZV-E10 附二维码）。
  const flow =
    providerId === "dji-action6"
      ? "ble"
      : providerId === "dji-mini5pro"
        ? "aircraft"
        : "wifi";
  const variant = flow;
  const qrEnabled = flow === "wifi";
  const copy = deviceCopy[providerId] ?? {
    title: "连接设备",
    description: "选定热点并确认后才会切换 Wi-Fi。",
    scan: {
      title: "搜索蓝牙设备",
      button: "搜索附近设备",
      working: "正在搜索…",
      help: "点击设备后先配对并扫描热点。",
    },
  };
  const steps =
    flow === "ble"
      ? ["蓝牙相机", "选择热点", "密码与连接"]
      : flow === "aircraft"
        ? ["蓝牙授权", "选择热点", "密码与连接"]
        : qrEnabled
          ? ["连接二维码", "选择热点", "密码与连接"]
          : ["选择热点", "密码与连接"];
  const candidate = state.wifiNetworks.find((n) => n.ssid === selected);
  const busy = state.busy || Boolean(working);
  const pending = [
    "choosing_wifi",
    "waiting_wifi",
    "connection_error",
  ].includes(state.phase);
  const preparing = state.phase === "preparing";
  const hasPicker =
    pending &&
    (state.wifiNetworks.length > 0 || state.phase === "choosing_wifi");
  const candidates = state.wifiNetworks.filter((n) => allNetworks || n.likely);
  const saved = Boolean(candidate?.hasSavedPassword && useSaved);
  // 二维码已读取该热点密码：留空提交即可，也可切换为手动输入。
  const qrReady =
    Boolean(candidate?.hasProvisionedPassword) && !useSaved && !manualPassword;
  const stepIndex =
    variant === "ble"
      ? !pending && state.phase !== "connecting"
        ? 0
        : candidate || state.phase === "connecting"
          ? 2
          : 1
      : candidate || state.phase === "connecting"
        ? steps.length - 1
        : pending
          ? qrEnabled
            ? 1
            : 0
          : 0;
  const atStart =
    flow !== "ble" && !pending && !state.busy && state.phase === "disconnected";

  useEffect(() => {
    if (!open || state.phase === "disconnected") {
      setPassword("");
      setVisible(false);
      setSelected("");
      setManualPassword(false);
      setFieldError("");
      setError("");
      setNotice("");
    }
  }, [open, state.phase]);
  useEffect(() => {
    if (candidate && !saved && !busy && open) passwordInput.current?.focus();
  }, [selected, saved, open]);

  useEffect(() => {
    if (open && state.phase === "connected") onOpenChange(false);
  }, [open, state.phase, onOpenChange]);

  async function run(method: Method, ...args: unknown[]) {
    if (inFlight.current) return false;
    inFlight.current = true;
    setWorking(method);
    setError("");
    setNotice("");
    try {
      await invoke(method, args);
      const next = (await devicesAPI.state()).connection;
      onState(next);
      return true;
    } catch (e) {
      setError(errorMessage(e));
      return false;
    } finally {
      setWorking("");
      inFlight.current = false;
    }
  }
  async function scan() {
    if (inFlight.current) return;
    inFlight.current = true;
    setWorking("Scan");
    setError("");
    setScanned(false);
    setDevices([]);
    try {
      setDevices(await devicesAPI.discover());
      setScanned(true);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setWorking("");
      inFlight.current = false;
    }
  }
  async function importQR(file: File | undefined) {
    if (!file || inFlight.current) return;
    inFlight.current = true;
    setWorking("ImportQR");
    setError("");
    try {
      const dataURL = await readQRPhoto(file);
      inFlight.current = false;
      await run("ImportQR", dataURL);
    } catch {
      setError("无法读取所选照片，请使用 JPG 或 PNG 格式的二维码照片。");
      inFlight.current = false;
      setWorking("");
    } finally {
      if (photoInput.current) photoInput.current.value = "";
    }
  }
  function choose(ssid: string, hasSaved: boolean) {
    setSelected(ssid);
    setUseSaved(hasSaved);
    setPassword("");
    setVisible(false);
    setManualPassword(false);
    setFieldError("");
    setError("");
    setNotice("");
  }
  async function join() {
    if (!candidate || busy) return;
    const length = new TextEncoder().encode(password).length;
    // 二维码已提供密码或二维码设备留空时，交给适配器处理。
    if (
      !saved &&
      !qrReady &&
      !(qrEnabled && password === "") &&
      (/[\u0000\r\n\t]/u.test(password) ||
        !((length >= 8 && length <= 63) || /^[0-9a-fA-F]{64}$/u.test(password)))
    ) {
      setFieldError(
        "请输入 8–63 字节的密码，或 64 位十六进制密钥。注意大小写，空格不会被自动删除。",
      );
      passwordInput.current?.focus();
      return;
    }
    setFieldError("");
    setVisible(false);
    const entered = password;
    const proceed = async () => {
      setPassword("");
      if (
        await run("JoinCameraWiFi", candidate.ssid, saved ? "" : entered, saved)
      )
        setUseSaved(true);
    };
    // 每次切换 Wi-Fi 前都提醒；用户选择“不再提醒”后跳过。
    if (localStorage.getItem(WIFI_SWITCH_NOTICE_KEY)) {
      await proceed();
      return;
    }
    pendingJoin.current = proceed;
    setSwitchNotice(true);
  }
  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent
          className="device-connection sm:max-w-[560px] max-h-[88vh] overflow-y-auto"
          onCloseAutoFocus={(e) => {
            e.preventDefault();
            returnFocus.current?.focus();
          }}
        >
          <DialogHeader>
            <DialogTitle>{copy.title}</DialogTitle>
            <DialogDescription>{copy.description}</DialogDescription>
          </DialogHeader>
          <ol className="connection-steps" aria-label="连接步骤">
            {steps.map((label, index) => (
              <li
                key={label}
                aria-current={index === stepIndex ? "step" : undefined}
              >
                {index + 1} {label}
              </li>
            ))}
          </ol>
          {((flow === "ble" &&
            !pending &&
            !preparing &&
            state.phase !== "connecting") ||
            (flow === "aircraft" && atStart)) && (
            <div className="connect-box">
              <div className="flex items-center gap-2 font-medium">
                <Bluetooth size={18} />
                {copy.scan.title}
              </div>
              <p className="text-xs muted mt-2 mb-4">{copy.scan.help}</p>
              <Button
                className="w-full"
                disabled={busy}
                onClick={() => void scan()}
              >
                {working === "Scan" ? (
                  <LoaderCircle className="animate-spin" />
                ) : (
                  <Radio />
                )}
                {working === "Scan" ? copy.scan.working : copy.scan.button}
              </Button>
              {scanned && !devices.length && (
                <p role="status" className="text-xs muted mt-4">
                  未找到相机。请检查蓝牙权限、相机电源，并确认手机已断开。
                </p>
              )}
              <div className="connection-list">
                {devices.map((d) => (
                  <button
                    key={d.id}
                    className="camera-option"
                    disabled={busy}
                    onClick={() => void run("PrepareCamera", d.id)}
                  >
                    <Camera size={19} />
                    <span>{d.name}</span>
                    <small>{d.rssi} dBm</small>
                    <ChevronRight size={16} />
                  </button>
                ))}
              </div>
            </div>
          )}
          {qrEnabled && atStart && (
            <div className="connect-box">
              <div className="flex items-center gap-2 font-medium">
                <QrCode size={18} />
                导入连接二维码
              </div>
              <p className="text-xs muted mt-2 mb-4">
                在相机回放界面进入「发送到智能手机」并选择素材，屏幕会显示二维码。拍照传到电脑后在此导入，也可以直接扫描热点。
                JPEG 尺寸、RAW+JPEG 和视频代理/原片请在相机发送设置中选择。
              </p>
              <input
                ref={photoInput}
                type="file"
                accept="image/jpeg,image/png"
                className="sr-only"
                aria-label="二维码照片文件"
                onChange={(e) => void importQR(e.target.files?.[0])}
              />
              <Button
                className="w-full"
                disabled={busy}
                onClick={() => photoInput.current?.click()}
              >
                {working === "ImportQR" ? (
                  <LoaderCircle className="animate-spin" />
                ) : (
                  <QrCode />
                )}
                {working === "ImportQR" ? "正在读取二维码…" : "选择二维码照片"}
              </Button>
            </div>
          )}
          {atStart && (
            <div className="connect-box">
              <div className="flex items-center gap-2 font-medium">
                <Wifi size={18} />
                搜索设备热点
              </div>
              <p className="text-xs muted mt-2 mb-4">
                在设备无线设置中开启 Wi-Fi 传输后扫描。选定热点并确认后才会切换
                Wi-Fi。
              </p>
              <Button
                className="w-full"
                disabled={busy}
                onClick={() => void run("ScanWiFi")}
              >
                {working === "ScanWiFi" ? (
                  <LoaderCircle className="animate-spin" />
                ) : (
                  <Radio />
                )}
                {working === "ScanWiFi" ? "正在扫描…" : "扫描附近热点"}
              </Button>
            </div>
          )}
          {hasPicker && !candidate && (
            <section className="connect-box" aria-label="附近的 Wi-Fi 热点">
              <div className="flex items-start justify-between gap-3">
                <div>
                  <div className="flex items-center gap-2 font-medium">
                    <Wifi size={18} />
                    选择设备热点
                  </div>
                  <p className="text-xs muted mt-2">
                    请对照设备屏幕上的无线信息确认名称，匹配提示仅供参考。
                  </p>
                </div>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={busy}
                  onClick={() => void run("ScanWiFi")}
                >
                  <RefreshCw />
                  重新扫描
                </Button>
              </div>
              <div className="wifi-options">
                {candidates.map((n) => (
                  <button
                    key={n.ssid}
                    className="wifi-option"
                    disabled={busy || !n.supported}
                    onClick={() => choose(n.ssid, n.hasSavedPassword)}
                  >
                    <Wifi size={19} />
                    <span>
                      <strong>{n.ssid}</strong>
                      <small>{n.reason}</small>
                      <small>
                        {n.security} · {n.rssi} dBm · 信道 {n.channel}
                        {n.hasSavedPassword ? " · 已记住密码" : ""}
                        {!n.supported ? " · 请在系统菜单连接" : ""}
                      </small>
                    </span>
                    <ChevronRight size={16} />
                  </button>
                ))}
              </div>
              {!candidates.length && (
                <p role="status" className="text-sm muted my-4">
                  {busy
                    ? "正在扫描，请稍候…"
                    : state.wifiNetworks.length
                      ? "没有发现名称匹配的热点，可展开其他热点核对。"
                      : "暂未扫描到可见热点。请保持设备开机、开启 Wi-Fi，并检查 macOS 定位服务权限后重试。"}
                </p>
              )}
              {state.wifiNetworks.some((n) => !n.likely) && (
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={busy}
                  onClick={() => setAllNetworks(!allNetworks)}
                >
                  {allNetworks
                    ? "仅显示可能的设备热点"
                    : `显示其他热点（${state.wifiNetworks.filter((n) => !n.likely).length}）`}
                </Button>
              )}
            </section>
          )}
          {candidate && (
            <form
              className="connect-box"
              noValidate
              onSubmit={(e) => {
                e.preventDefault();
                void join();
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter" && e.nativeEvent.isComposing)
                  e.preventDefault();
              }}
            >
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="mb-3"
                disabled={busy}
                onClick={() => choose("", false)}
              >
                <ChevronLeft />
                返回热点列表
              </Button>
              <div className="flex items-start gap-3">
                <Wifi size={21} className="shrink-0 mt-1" />
                <div className="min-w-0">
                  <strong className="break-all">{candidate.ssid}</strong>
                  <p className="text-xs muted mt-1">
                    {candidate.security} · {candidate.rssi} dBm
                  </p>
                </div>
              </div>
              {saved ? (
                <div className="saved-credential">
                  <ShieldCheck size={18} />
                  <span>已在本机钥匙串记住密码，连接时直接使用。</span>
                </div>
              ) : qrReady ? (
                <div className="mt-5">
                  <div className="saved-credential">
                    <QrCode size={18} />
                    <span>
                      已从二维码照片读取该热点的密码，无需输入，确认后直接连接。
                    </span>
                  </div>
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    className="mt-2"
                    disabled={busy}
                    onClick={() => {
                      setManualPassword(true);
                      setPassword("");
                      setVisible(false);
                    }}
                  >
                    <KeyRound />
                    手动输入密码
                  </Button>
                </div>
              ) : (
                <div className="mt-5">
                  <label
                    htmlFor="camera-wifi-password"
                    className="text-sm font-medium"
                  >
                    设备 Wi-Fi 密码
                  </label>
                  <div className="wifi-password-field">
                    <Input
                      ref={passwordInput}
                      id="camera-wifi-password"
                      type={visible ? "text" : "password"}
                      autoComplete="current-password"
                      autoCapitalize="none"
                      spellCheck={false}
                      disabled={busy}
                      value={password}
                      aria-invalid={Boolean(fieldError)}
                      aria-describedby={
                        fieldError
                          ? "wifi-password-error wifi-password-help"
                          : "wifi-password-help"
                      }
                      onChange={(e) => {
                        setPassword(e.target.value);
                        setFieldError("");
                      }}
                    />
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      aria-label={visible ? "隐藏密码" : "显示密码"}
                      aria-pressed={visible}
                      disabled={busy}
                      onClick={() => setVisible(!visible)}
                    >
                      {visible ? <EyeOff /> : <Eye />}
                    </Button>
                  </div>
                  {fieldError && (
                    <p
                      id="wifi-password-error"
                      role="alert"
                      className="field-error"
                    >
                      {fieldError}
                    </p>
                  )}
                  <p id="wifi-password-help" className="text-xs muted mt-2">
                    密码显示在设备无线信息或二维码中。已导入二维码时可以留空，
                    将使用二维码中的密码。
                  </p>
                  <p className="text-xs muted mt-2">
                    提交后会在这台 Mac
                    上记住本次密码，无论连接是否成功，下次可直接使用。
                    密码仅存入本机钥匙串。
                  </p>
                </div>
              )}
              {candidate.hasSavedPassword && (
                <div className="flex flex-wrap gap-2 mt-3">
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    disabled={busy}
                    onClick={() => {
                      setUseSaved(!saved);
                      setPassword("");
                      setVisible(false);
                      setFieldError("");
                    }}
                  >
                    <KeyRound />
                    {saved ? "使用其他密码" : "使用已记住的密码"}
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    disabled={busy}
                    onClick={async () => {
                      if (await run("ForgetWiFiPassword", candidate.ssid)) {
                        setUseSaved(false);
                        setPassword("");
                        setNotice(
                          "已忘记该设备热点的密码，下次连接需要重新输入。",
                        );
                      }
                    }}
                  >
                    忘记密码
                  </Button>
                </div>
              )}
              <p className="text-xs muted mt-5 mb-3">
                确认后将切换到此热点，Mac 的 Wi-Fi 互联网连接可能暂时中断。
              </p>
              <Button type="submit" className="w-full" disabled={busy}>
                {state.phase === "connecting" ? (
                  <LoaderCircle className="animate-spin" />
                ) : (
                  <Wifi />
                )}
                {state.phase === "connecting"
                  ? "正在连接…"
                  : saved
                    ? "使用已记住的密码连接"
                    : qrReady
                      ? "使用二维码中的密码连接"
                      : "连接此热点"}
              </Button>
            </form>
          )}
          {(preparing || state.phase === "connecting" || state.busy) && (
            <div className="message" role="status">
              <LoaderCircle className="animate-spin" />
              {state.message}
            </div>
          )}
          {!state.busy &&
            ["waiting_wifi", "connection_error"].includes(state.phase) && (
              <div className="message error" role="alert">
                {state.message}。
                {state.phase === "connection_error"
                  ? "热点连接信息已保留。请按上述原因检查素材连接，详细过程可在“诊断日志”中查看。"
                  : state.wifiNetworks.length > 0
                    ? "连接信息已保留，可核对热点密码或重试。"
                    : "当前连接信息已保留，可检查热点后重试。"}
              </div>
            )}
          {!state.busy &&
            state.phase === "choosing_wifi" &&
            !state.message.includes("请选择") &&
            !state.message.includes("二维码") && (
              <div role="alert" className="message error">
                {state.message}
              </div>
            )}
          {!state.busy &&
            state.phase === "disconnected" &&
            state.message !== "连接相机，开始导入原始素材" &&
            state.message !== "连接设备，开始导入原始素材" && (
              <p role="status" className="text-sm muted">
                {state.message}
              </p>
            )}
          {state.warning && (
            <p role="status" className="text-xs muted">
              {state.warning}
            </p>
          )}
          {notice && (
            <p role="status" className="text-sm">
              {notice}
            </p>
          )}
          {error && (
            <div role="alert" className="message error">
              {error}
            </div>
          )}
          <details className="manual-wifi">
            <summary>已在系统 Wi-Fi 菜单连接？</summary>
            <p className="text-xs muted mt-3 mb-3">
              确认 Mac
              已加入设备热点后可直接接续。没有识别设备身份的会话不支持跨重连续传。
            </p>
            <Button
              variant="outline"
              className="w-full"
              disabled={busy}
              onClick={() => void run("Connect", "", true)}
            >
              <Wifi />
              {state.phase === "connection_error"
                ? "重试素材连接"
                : "我已连接设备 Wi-Fi，继续"}
            </Button>
          </details>
          {(pending || preparing || state.phase === "connecting") && (
            <div>
              <Button
                variant="ghost"
                disabled={Boolean(working)}
                onClick={() => void run("Disconnect")}
              >
                取消本次连接
              </Button>
              <p className="text-xs muted mt-2">
                关闭面板会保留连接进度；取消本次连接才会释放会话。
              </p>
            </div>
          )}
        </DialogContent>
      </Dialog>
      <Dialog
        open={switchNotice}
        onOpenChange={(next) => {
          if (!next) {
            // 仅关闭提醒；保留已填密码，用户可返回修改或重新提交。
            setSwitchNotice(false);
            pendingJoin.current = null;
          }
        }}
      >
        <DialogContent className="sm:max-w-[460px]">
          <DialogHeader>
            <DialogTitle>即将切换 Mac 的 Wi-Fi</DialogTitle>
            <DialogDescription>
              连接「{candidate?.ssid}」需要把 Mac
              切换到设备热点，期间互联网连接可能暂时中断；断开设备后会尝试恢复原网络。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => {
                localStorage.setItem(WIFI_SWITCH_NOTICE_KEY, "1");
                setSwitchNotice(false);
                pendingJoin.current?.();
                pendingJoin.current = null;
              }}
            >
              不再提醒
            </Button>
            <Button
              disabled={busy}
              onClick={() => {
                setSwitchNotice(false);
                pendingJoin.current?.();
                pendingJoin.current = null;
              }}
            >
              继续连接
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

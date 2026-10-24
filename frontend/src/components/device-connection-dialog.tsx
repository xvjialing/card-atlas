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
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  devicesAPI,
  errorMessage,
  type Device,
  type Session,
} from "@/lib/devices";
type Method =
  | "PrepareCamera"
  | "ScanWiFi"
  | "JoinCameraWiFi"
  | "ForgetWiFiPassword"
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
    case "Connect":
      return devicesAPI.connect({ action: "manual" });
    case "Disconnect":
      return devicesAPI.disconnect();
  }
}

export function DeviceConnectionDialog({
  open,
  onOpenChange,
  state,
  onState,
  returnFocus,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  state: Session;
  onState: (state: Session) => void;
  returnFocus: RefObject<HTMLElement | null>;
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
  const [fieldError, setFieldError] = useState("");
  const passwordInput = useRef<HTMLInputElement>(null);
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

  useEffect(() => {
    if (!open || state.phase === "disconnected") {
      setPassword("");
      setVisible(false);
      setSelected("");
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
  function choose(ssid: string, hasSaved: boolean) {
    setSelected(ssid);
    setUseSaved(hasSaved);
    setPassword("");
    setVisible(false);
    setFieldError("");
    setError("");
    setNotice("");
  }
  async function join() {
    if (!candidate || busy) return;
    const length = new TextEncoder().encode(password).length;
    if (
      !saved &&
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
    setPassword("");
    if (
      await run("JoinCameraWiFi", candidate.ssid, saved ? "" : entered, saved)
    )
      setUseSaved(true);
  }
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="device-connection sm:max-w-[560px] max-h-[88vh] overflow-y-auto"
        onCloseAutoFocus={(e) => {
          e.preventDefault();
          returnFocus.current?.focus();
        }}
      >
        <DialogHeader>
          <DialogTitle>连接 DJI Osmo Action 6</DialogTitle>
          <DialogDescription>
            相机放在 Mac 附近，并退出手机 DJI Mimo。选定热点并确认后才会切换
            Wi-Fi。
          </DialogDescription>
        </DialogHeader>
        <ol className="connection-steps" aria-label="连接步骤">
          <li
            aria-current={
              !pending && state.phase !== "connecting" ? "step" : undefined
            }
          >
            1 蓝牙相机
          </li>
          <li aria-current={pending && !candidate ? "step" : undefined}>
            2 选择热点
          </li>
          <li
            aria-current={
              candidate || state.phase === "connecting" ? "step" : undefined
            }
          >
            3 密码与连接
          </li>
        </ol>
        {!pending && !preparing && state.phase !== "connecting" && (
          <div className="connect-box">
            <div className="flex items-center gap-2 font-medium">
              <Bluetooth size={18} />
              搜索蓝牙相机
            </div>
            <p className="text-xs muted mt-2 mb-4">
              点击设备后先配对并扫描热点，首次配对请在相机屏幕上确认。
            </p>
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
              {working === "Scan" ? "正在搜索…" : "搜索附近相机"}
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
        {hasPicker && !candidate && (
          <section className="connect-box" aria-label="附近的 Wi-Fi 热点">
            <div className="flex items-start justify-between gap-3">
              <div>
                <div className="flex items-center gap-2 font-medium">
                  <Wifi size={18} />
                  选择相机热点
                </div>
                <p className="text-xs muted mt-2">
                  已配对：{state.deviceName}
                  。请对照相机「无线信息」确认名称，匹配提示仅供参考。
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
                    : "暂未扫描到可见热点。请保持相机开机、开启 Wi-Fi，并检查 macOS 定位服务权限后重试。"}
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
                  ? "仅显示可能的相机热点"
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
                  {candidate.security} · {candidate.rssi} dBm ·{" "}
                  {state.deviceName}
                </p>
              </div>
            </div>
            {saved ? (
              <div className="saved-credential">
                <ShieldCheck size={18} />
                <span>已在本机钥匙串记住密码，连接时直接使用。</span>
              </div>
            ) : (
              <div className="mt-5">
                <label
                  htmlFor="camera-wifi-password"
                  className="text-sm font-medium"
                >
                  相机 Wi-Fi 密码
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
                  相机屏幕下拉 → 系统设置 → 无线连接 →
                  无线信息。请输入这里显示的密码。
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
                        "已忘记该相机热点的密码，下次连接需要重新输入。",
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
              {state.wifiNetworks.length > 0
                ? "蓝牙会话已保留，可修改密码或重试。"
                : "当前连接信息已保留，可检查热点后重试。"}
            </div>
          )}
        {!state.busy &&
          state.phase === "choosing_wifi" &&
          state.message !== "请选择相机热点，确认后才会切换 Wi-Fi" && (
            <div role="alert" className="message error">
              {state.message}
            </div>
          )}
        {!state.busy &&
          state.phase === "disconnected" &&
          state.message !== "连接相机，开始导入原始素材" && (
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
            已加入相机热点后可直接接续。若没有蓝牙配对，续传仅限当前手动会话。
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
              : "我已连接相机 Wi-Fi，继续"}
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
  );
}

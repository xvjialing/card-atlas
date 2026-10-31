import { useEffect, useRef, useState } from "react";
import {
  CircleAlert,
  Info,
  LoaderCircle,
  RefreshCw,
  WifiOff,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { deviceMediaURL, type Media } from "@/lib/devices";
import { bytes } from "@/lib/types";

const PREVIEW_LOAD_TIMEOUT = 20_000;
type LoadState = "loading" | "ready" | "failed" | "timeout";
type Dimensions = { width: number; height: number };

export function DevicePreviewDialog({
  file,
  provider,
  session,
  connected,
  onClose,
  returnFocus,
}: {
  file: Media | null;
  provider: string;
  session: string;
  connected: boolean;
  onClose: () => void;
  returnFocus: { current: HTMLButtonElement | null };
}) {
  const dialogRef = useRef<HTMLDivElement>(null);
  const lastFile = useRef(file);
  useEffect(() => {
    if (file) lastFile.current = file;
  }, [file]);
  // 关闭动画期间保留布局，媒体读取则立即停止。
  const shown = file ?? lastFile.current;

  return (
    <Dialog open={Boolean(file)} onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        ref={dialogRef}
        className="device-preview-dialog"
        onOpenAutoFocus={(event) => {
          event.preventDefault();
          dialogRef.current
            ?.querySelector<HTMLButtonElement>("[data-slot='dialog-close']")
            ?.focus({ preventScroll: true });
        }}
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          returnFocus.current?.focus({ preventScroll: true });
        }}
      >
        <DialogHeader className="device-preview-header">
          <DialogDescription>设备素材预览</DialogDescription>
          <DialogTitle className="device-preview-filename">
            {shown?.name ?? "素材预览"}
          </DialogTitle>
        </DialogHeader>
        {shown && (
          <PreviewBody
            key={`${provider}:${session}:${shown.id}:${connected}`}
            file={shown}
            provider={provider}
            session={session}
            connected={connected}
            active={Boolean(file)}
            onClose={onClose}
          />
        )}
        <p className="device-preview-footer">
          <Info size={14} aria-hidden="true" />
          仅预览设备中的素材，不会自动保存到本机或转码。
        </p>
      </DialogContent>
    </Dialog>
  );
}

function PreviewBody({
  file,
  provider,
  session,
  connected,
  active,
  onClose,
}: {
  file: Media;
  provider: string;
  session: string;
  connected: boolean;
  active: boolean;
  onClose: () => void;
}) {
  const [loadState, setLoadState] = useState<LoadState>("loading");
  const [attempt, setAttempt] = useState(0);
  const [dimensions, setDimensions] = useState<Dimensions | null>(null);
  const [sizing, setSizing] = useState<"fit" | "actual">("fit");
  const [buffering, setBuffering] = useState(false);
  const videoRef = useRef<HTMLVideoElement>(null);
  const video = file.kind === "video";
  const format =
    file.name.match(/\.([a-z0-9]{1,8})$/i)?.[1].toUpperCase() ?? "文件";
  const raw = /^(ARW|DNG)$/.test(format);
  const failed = loadState === "failed" || loadState === "timeout";
  const src = deviceMediaURL(provider, file.id, video || !raw, session);

  useEffect(() => {
    if (
      !connected ||
      !active ||
      failed ||
      (loadState !== "loading" && !buffering)
    )
      return;
    const timeout = window.setTimeout(() => {
      setLoadState("timeout");
    }, PREVIEW_LOAD_TIMEOUT);
    return () => window.clearTimeout(timeout);
  }, [attempt, connected, active, loadState, buffering, failed]);

  const retry = () => {
    setLoadState("loading");
    setDimensions(null);
    setBuffering(false);
    setAttempt((value) => value + 1);
  };
  const ready = (width: number, height: number) => {
    if (width > 0 && height > 0) setDimensions({ width, height });
    setLoadState("ready");
  };

  return (
    <div className="device-preview-layout">
      <section className="device-preview-viewer" aria-label="素材预览">
        <div className="device-preview-toolbar">
          <span>{raw ? "相机缩略图" : video ? "视频预览" : "图片预览"}</span>
          {!video && (
            <div
              className="device-preview-sizing"
              role="group"
              aria-label="图片显示方式"
            >
              <Button
                size="xs"
                variant="ghost"
                aria-pressed={sizing === "fit"}
                onClick={() => setSizing("fit")}
              >
                适应画布
              </Button>
              <Button
                size="xs"
                variant="ghost"
                aria-pressed={sizing === "actual"}
                onClick={() => setSizing("actual")}
              >
                实际尺寸
              </Button>
            </div>
          )}
        </div>
        <div
          className="device-preview-stage"
          role="region"
          aria-label="素材预览画布"
          tabIndex={
            !video && sizing === "actual" && loadState === "ready"
              ? 0
              : undefined
          }
          data-sizing={sizing}
          data-state={!connected ? "offline" : loadState}
        >
          {connected &&
            !failed &&
            active &&
            (video ? (
              <PreviewVideo
                key={attempt}
                elementRef={videoRef}
                src={src}
                name={file.name}
                ready={loadState === "ready"}
                onReady={ready}
                onFailed={() => setLoadState("failed")}
                onBuffering={setBuffering}
              />
            ) : (
              <img
                key={attempt}
                className="device-preview-image"
                src={src}
                alt={raw ? `${format} 相机缩略图` : file.name}
                onLoad={(event) =>
                  ready(
                    event.currentTarget.naturalWidth,
                    event.currentTarget.naturalHeight,
                  )
                }
                onError={() => setLoadState("failed")}
              />
            ))}
          {connected && loadState === "loading" && (
            <div className="device-preview-status" role="status">
              <LoaderCircle
                className="animate-spin"
                size={28}
                aria-hidden="true"
              />
              <strong>
                {video
                  ? "正在读取视频"
                  : raw
                    ? "正在读取相机缩略图"
                    : "正在读取图片"}
              </strong>
              <p>请保持设备连接</p>
              {video && (
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => {
                    // 部分浏览器需用户手势才读取视频，加载时仍提供播放入口。
                    void videoRef.current?.play().catch((error: unknown) => {
                      if (
                        !(error instanceof DOMException) ||
                        error.name !== "AbortError"
                      )
                        setLoadState("failed");
                    });
                  }}
                >
                  播放视频
                </Button>
              )}
            </div>
          )}
          {(!connected || failed) && (
            <div className="device-preview-status" role="alert">
              {!connected ? (
                <WifiOff size={30} aria-hidden="true" />
              ) : (
                <CircleAlert size={30} aria-hidden="true" />
              )}
              <strong>
                {!connected
                  ? "设备连接已中断"
                  : loadState === "timeout"
                    ? "读取时间较长"
                    : video
                      ? "暂时无法播放视频"
                      : "暂时无法显示图片"}
              </strong>
              <p>
                {!connected
                  ? "重新连接设备后，再打开这份素材预览。"
                  : loadState === "timeout"
                    ? "设备尚未返回预览，请保持连接后重新加载。"
                    : "请检查设备连接后重试；也可导入原文件，再用本机应用查看。"}
              </p>
              <Button
                variant="secondary"
                size="sm"
                onClick={connected ? retry : onClose}
              >
                {connected && <RefreshCw size={14} aria-hidden="true" />}
                {connected ? "重新加载" : "返回素材列表"}
              </Button>
            </div>
          )}
          {connected && loadState === "ready" && buffering && video && (
            <div className="device-preview-buffering" role="status">
              正在缓冲视频…
            </div>
          )}
        </div>
      </section>
      <aside className="device-preview-info" aria-label="素材信息">
        <div className="device-preview-info-title">
          <h3>素材信息</h3>
          <Badge variant="secondary">{format}</Badge>
        </div>
        <dl className="device-preview-metadata">
          <div>
            <dt>拍摄时间</dt>
            <dd>{file.date || "拍摄时间未知"}</dd>
          </div>
          <div>
            <dt>{file.sizeEstimated ? "估算大小" : "文件大小"}</dt>
            <dd>{file.size > 0 ? bytes(file.size) : "尚未获取"}</dd>
          </div>
          <div>
            <dt>存储位置</dt>
            <dd>{file.storageName || "设备存储"}</dd>
          </div>
          <div>
            <dt>{raw ? "缩略图尺寸" : "预览尺寸"}</dt>
            <dd>
              {dimensions
                ? `${dimensions.width} × ${dimensions.height}`
                : loadState === "loading" && connected
                  ? "读取中"
                  : "—"}
            </dd>
          </div>
        </dl>
        <div className="device-preview-quality">
          <Info size={16} aria-hidden="true" />
          <p>
            {raw
              ? `当前显示相机提供的缩略图，放大不提升清晰度。导入后可查看 ${format} 原文件。`
              : video
                ? provider === "sony-zve10"
                  ? "原片或代理由相机发送设置决定，预览不会改变视频画质。"
                  : "优先读取设备预览，缺失时读取原视频；预览不会转码。"
                : "按需读取设备中的照片，预览不会修改原文件。"}
          </p>
        </div>
        <details className="device-preview-location">
          <summary>源文件位置</summary>
          <p>{file.path}</p>
        </details>
      </aside>
    </div>
  );
}

function PreviewVideo({
  elementRef,
  src,
  name,
  ready,
  onReady,
  onFailed,
  onBuffering,
}: {
  elementRef: { current: HTMLVideoElement | null };
  src: string;
  name: string;
  ready: boolean;
  onReady: (width: number, height: number) => void;
  onFailed: () => void;
  onBuffering: (value: boolean) => void;
}) {
  useEffect(() => {
    const element = elementRef.current;
    // 在 effect 中成对设置与释放资源，兼容 StrictMode 的重复挂载检查。
    if (element) {
      element.src = src;
      element.load();
    }
    return () => {
      element?.pause();
      element?.removeAttribute("src");
      element?.load();
    };
  }, [src, elementRef]);
  return (
    <video
      ref={elementRef}
      className="device-preview-video"
      data-ready={ready}
      aria-label={name}
      controls
      playsInline
      preload="metadata"
      onLoadedMetadata={(event) =>
        onReady(event.currentTarget.videoWidth, event.currentTarget.videoHeight)
      }
      onCanPlay={() => onBuffering(false)}
      onPlaying={() => onBuffering(false)}
      onWaiting={(event) => onBuffering(!event.currentTarget.paused)}
      onPause={() => onBuffering(false)}
      onError={onFailed}
    />
  );
}

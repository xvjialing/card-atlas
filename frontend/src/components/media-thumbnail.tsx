import { useEffect, useRef, useState } from "react";
import { FileImage, Film, LoaderCircle, Play } from "lucide-react";
import { api } from "@/lib/api";
import type { MediaFile, Thumbnail } from "@/lib/types";

const cache = new Map<string, Thumbnail>();

export function MediaThumbnail({
  file,
  scanPath,
  demo,
}: {
  file: MediaFile;
  scanPath: string;
  demo: boolean;
}) {
  const container = useRef<HTMLSpanElement>(null);
  const [result, setResult] = useState<Thumbnail | null>(null);
  const [loading, setLoading] = useState(false);
  useEffect(() => {
    let active = true;
    let requested = false;
    setResult(null);
    setLoading(false);
    if (demo) {
      setResult({ dataURL: "", message: "演示素材未关联真实文件" });
      return;
    }
    const key = `${scanPath}/${file.id}/${file.modified}`;
    const cached = cache.get(key);
    if (cached) {
      setResult(cached);
      return;
    }
    // 表格滚动到可见位置才请求，后台服务同时最多解码两个文件。
    const observer = new IntersectionObserver(
      (entries) => {
        if (!entries.some((entry) => entry.isIntersecting) || requested) return;
        requested = true;
        observer.disconnect();
        setLoading(true);
        api
          .thumbnail(file.id)
          .then((thumbnail) => {
            cache.set(key, thumbnail);
            if (cache.size > 128) cache.delete(cache.keys().next().value!);
            if (active) setResult(thumbnail);
          })
          .catch(() => {
            if (active)
              setResult({
                dataURL: "",
                message: "文件无法读取，缩略图暂不可用",
              });
          })
          .finally(() => {
            if (active) setLoading(false);
          });
      },
      { rootMargin: "80px" },
    );
    if (container.current) observer.observe(container.current);
    return () => {
      active = false;
      observer.disconnect();
    };
  }, [file.id, file.modified, scanPath, demo]);
  return (
    <span
      ref={container}
      className={`media-thumbnail ${file.kind} ${result?.dataURL ? "has-image" : ""}`}
      title={result?.message || `${file.name} 的缩略图`}
    >
      {result?.dataURL ? (
        <img
          src={result.dataURL}
          alt={`${file.name} 的缩略图`}
          width={60}
          height={44}
          onError={() => setResult({ dataURL: "", message: "缩略图显示失败" })}
        />
      ) : loading ? (
        <LoaderCircle className="spin" size={17} aria-label="正在加载缩略图" />
      ) : file.kind === "photo" ? (
        <FileImage size={20} aria-label="照片缩略图暂不可用" />
      ) : (
        <Film size={20} aria-label="视频缩略图暂不可用" />
      )}
      <small>{file.extension}</small>
      {result?.dataURL && file.kind === "video" && (
        <span className="thumbnail-play">
          <Play size={10} fill="currentColor" />
        </span>
      )}
    </span>
  );
}

import { useEffect, useMemo, useRef, useState } from "react";
import {
  CalendarDays,
  Camera,
  FileImage,
  Film,
  LayoutGrid,
  List,
  Play,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { deviceMediaURL, type Media } from "@/lib/devices";
import { bytes, number } from "@/lib/types";
import { captureDate, groupByCaptureDay } from "@/lib/media-timeline";
import { mediaSelectionState } from "@/lib/media-selection";

type View = "list" | "grid" | "timeline";
type PreviewProps = {
  provider: string;
  session: string;
  connected: boolean;
  revision: number;
};
type Props = PreviewProps & {
  files: Media[];
  visible: Media[];
  page: number;
  selected: ReadonlySet<string>;
  imported: ReadonlySet<string>;
  loading: boolean;
  hasMedia: boolean;
  disabled: boolean;
  storageNames: ReadonlyMap<string, string>;
  onToggle: (ids: string[], checked: boolean) => void;
  onPreview: (file: Media, trigger: HTMLButtonElement) => void;
};

export function DeviceMediaBrowser(props: Props) {
  const {
    files,
    visible,
    selected,
    imported,
    onToggle,
    onPreview,
    storageNames,
  } = props;
  const [view, setView] = useState<View>("grid");
  const scroller = useRef<HTMLDivElement>(null);
  const allDays = useMemo(
    () =>
      new Map(
        groupByCaptureDay(files).map((group) => [group.day, group.media]),
      ),
    [files],
  );
  const groups = groupByCaptureDay(visible);
  useEffect(() => {
    scroller.current?.scrollTo({ top: 0, left: 0 });
  }, [view, props.page]);
  const alreadyImported = (file: Media) =>
    imported.has(`${file.id}/${file.date}/${file.size}`);
  const storageLabel = (file: Media) =>
    storageNames.get(file.storageId) || file.storageName;
  function card(file: Media) {
    return (
      <article
        className="device-media-card"
        key={file.id}
        data-media-id={file.id}
        data-selected={selected.has(file.id)}
      >
        <button
          className="device-card-preview"
          aria-label={`预览 ${file.name}`}
          onClick={(event) => onPreview(file, event.currentTarget)}
        >
          <DeviceThumbnail {...props} file={file} large />
          <span className="device-card-format">
            {file.kind === "video" ? (
              <>
                <Play size={12} />
                {formatDuration(file.duration)}
              </>
            ) : (
              file.name.split(".").pop()?.toUpperCase() || "照片"
            )}
          </span>
        </button>
        <label className="device-card-check">
          <Checkbox
            checked={selected.has(file.id)}
            disabled={props.disabled}
            aria-label={`选择 ${file.name}`}
            onCheckedChange={(value) => onToggle([file.id], value === true)}
          />
          <span className="sr-only">选择 {file.name}</span>
        </label>
        <div className="device-card-info">
          <button
            className="device-card-name"
            title={file.name}
            onClick={(event) => onPreview(file, event.currentTarget)}
          >
            {file.name}
          </button>
          <div>
            <span>{bytes(file.size)}</span>
            <span>{storageLabel(file)}</span>
          </div>
          <time dateTime={captureDate(file.date)?.replace(" ", "T")}>
            {captureDate(file.date) || "拍摄时间未知"}
          </time>
          {alreadyImported(file) && (
            <span className="device-card-imported">已有导入记录</span>
          )}
        </div>
      </article>
    );
  }
  return (
    <>
      <div className="device-view-toolbar">
        <span>拍摄时间 · 从新到旧</span>
        <div
          className="device-segmented"
          role="group"
          aria-label="素材展示方式"
        >
          {(
            [
              ["list", "列表", List],
              ["grid", "网格", LayoutGrid],
              ["timeline", "时间线", CalendarDays],
            ] as const
          ).map(([value, label, Icon]) => (
            <Button
              key={value}
              size="sm"
              variant="ghost"
              aria-pressed={view === value}
              onClick={() => setView(value)}
            >
              <Icon size={15} />
              {label}
            </Button>
          ))}
        </div>
      </div>
      <div
        ref={scroller}
        className={`device-media-scroll ${view}`}
        aria-busy={props.loading}
        aria-label="设备素材浏览区域"
        tabIndex={0}
      >
        {!visible.length ? (
          <div className="device-empty" role="status">
            <Camera size={30} />
            <strong>
              {props.loading
                ? "正在读取设备素材…"
                : props.hasMedia
                  ? "没有符合筛选条件的素材"
                  : "设备内暂未发现可导入素材"}
            </strong>
            <p>
              {props.hasMedia
                ? "调整筛选条件；此前选择的素材仍然保留。"
                : "确认设备中已有照片或视频，再刷新素材。"}
            </p>
          </div>
        ) : view === "list" ? (
          <table className="device-table">
            <thead>
              <tr>
                <th scope="col">
                  <span className="sr-only">选择</span>
                </th>
                <th scope="col">素材</th>
                <th scope="col">拍摄日期 ↓</th>
                <th scope="col">存储位置</th>
                <th scope="col">清单容量</th>
              </tr>
            </thead>
            <tbody>
              {visible.map((file) => (
                <tr
                  key={file.id}
                  data-media-id={file.id}
                  data-selected={selected.has(file.id)}
                >
                  <td>
                    <Checkbox
                      aria-label={`选择 ${file.name}`}
                      checked={selected.has(file.id)}
                      disabled={props.disabled}
                      onCheckedChange={(value) =>
                        onToggle([file.id], value === true)
                      }
                    />
                  </td>
                  <td>
                    <button
                      className="device-file-button"
                      onClick={(event) => onPreview(file, event.currentTarget)}
                    >
                      <DeviceThumbnail {...props} file={file} />
                      <span>
                        <strong>{file.name}</strong>
                        <small>
                          {file.kind === "video"
                            ? `视频 · ${formatDuration(file.duration)}`
                            : file.name.split(".").pop() || "照片"}
                          {alreadyImported(file) ? " · 有导入记录" : ""}
                        </small>
                      </span>
                    </button>
                  </td>
                  <td>
                    <time>{captureDate(file.date) || "日期未知"}</time>
                  </td>
                  <td>{storageLabel(file)}</td>
                  <td>{bytes(file.size)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : view === "grid" ? (
          <div className="device-media-grid">{visible.map(card)}</div>
        ) : (
          <div className="device-timeline">
            <p
              className="device-timeline-scope"
              id="device-day-selection-scope"
            >
              按天全选包含当前筛选条件下的所有分页，不会选入其他类型或存储的素材。
            </p>
            {groups.map((group) => {
              const allMedia = allDays.get(group.day) ?? group.media;
              return (
                <section
                  className="device-timeline-day"
                  key={group.day}
                  aria-label={group.label}
                >
                  <div className="device-day-heading">
                    <div>
                      <h3>
                        {group.day === "unknown" ? (
                          group.label
                        ) : (
                          <time dateTime={group.day}>{group.label}</time>
                        )}
                      </h3>
                      <p>
                        本页 {number(group.media.length)} / 当天{" "}
                        {number(allMedia.length)} 项
                      </p>
                    </div>
                    <label>
                      <Checkbox
                        aria-label={`选择${group.label}的全部素材`}
                        aria-describedby="device-day-selection-scope"
                        checked={mediaSelectionState(selected, allMedia)}
                        disabled={props.disabled}
                        onCheckedChange={(value) =>
                          onToggle(
                            allMedia.map((file) => file.id),
                            value === true,
                          )
                        }
                      />
                      <span>
                        {group.day === "unknown" ? "全选本组" : "全选当天"}
                      </span>
                    </label>
                  </div>
                  <div className="device-media-grid">
                    {group.media.map(card)}
                  </div>
                </section>
              );
            })}
          </div>
        )}
      </div>
    </>
  );
}

function DeviceThumbnail({
  file,
  provider,
  session,
  connected,
  revision,
  large = false,
}: PreviewProps & { file: Media; large?: boolean }) {
  const [failed, setFailed] = useState(false);
  useEffect(() => setFailed(false), [file.id, session, revision]);
  return (
    <span
      className={
        large
          ? `device-card-image ${file.kind}`
          : `media-thumbnail ${file.kind}`
      }
    >
      {file.thumbnail && connected && !failed ? (
        <img
          loading="lazy"
          src={`${deviceMediaURL(provider, file.id, false, session)}&revision=${revision}`}
          alt={`${file.name} 缩略图`}
          width={large ? 320 : 60}
          height={large ? 200 : 44}
          onError={() => setFailed(true)}
        />
      ) : (
        <>
          {file.kind === "video" ? (
            <Film size={large ? 34 : 21} aria-label="视频缩略图暂不可用" />
          ) : (
            <FileImage size={large ? 34 : 21} aria-label="照片缩略图暂不可用" />
          )}
          {large && <small>缩略图暂不可用</small>}
        </>
      )}
    </span>
  );
}
function formatDuration(value: number) {
  return `${Math.floor(value / 60)
    .toString()
    .padStart(2, "0")}:${(value % 60).toString().padStart(2, "0")}`;
}

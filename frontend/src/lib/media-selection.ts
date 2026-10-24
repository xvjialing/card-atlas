import type { Media } from "./devices";

type SelectableMedia = Pick<Media, "id">;

export function mediaSelectionState(
  selection: ReadonlySet<string>,
  media: readonly SelectableMedia[],
): boolean | "indeterminate" {
  const selected = media.filter((item) => selection.has(item.id)).length;
  if (selected === 0) return false;
  return selected === media.length ? true : "indeterminate";
}

// 仅更新指定范围，保留其他日期、分页和筛选之外已有的选择。
export function setMediaSelection(
  selection: ReadonlySet<string>,
  media: readonly SelectableMedia[],
  checked: boolean,
): Set<string> {
  const next = new Set(selection);
  for (const item of media) {
    if (checked) next.add(item.id);
    else next.delete(item.id);
  }
  return next;
}

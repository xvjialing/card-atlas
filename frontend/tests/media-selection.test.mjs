import assert from "node:assert/strict";
import test from "node:test";
import {
  mediaSelectionState,
  setMediaSelection,
} from "../src/lib/media-selection.ts";
import {
  groupByCaptureDay,
  sortByCaptureDate,
} from "../src/lib/media-timeline.ts";

const media = Array.from({ length: 40 }, (_, i) => ({
  id: String(i),
  date: i < 30 ? "2026-10-23 12:30:00" : "2026-10-22 12:30:00",
  kind: i % 3 === 0 ? "photo" : "video",
  storage: i % 2,
}));

test("按天选择覆盖跨页素材，取消仅影响当天并保持原选择不可变", () => {
  const ordered = sortByCaptureDate(media);
  const day = groupByCaptureDay(ordered)[0].media;
  const initial = new Set(["0", "35"]);
  assert.equal(mediaSelectionState(initial, day), "indeterminate");

  const selected = setMediaSelection(initial, day, true);
  assert.equal(selected.size, 31);
  assert.equal(
    mediaSelectionState(
      selected,
      ordered.slice(24).filter((m) => m.date.startsWith("2026-10-23")),
    ),
    true,
  );
  assert.equal(mediaSelectionState(selected, day), true);
  assert.deepEqual([...initial], ["0", "35"]);

  const cleared = setMediaSelection(selected, day, false);
  assert.deepEqual([...cleared], ["35"]);
  assert.equal(mediaSelectionState(cleared, day), false);
  assert.equal(selected.size, 31);
});

test("按天选择遵循当前类型、存储及搜索筛选，保留筛选外已有选择", () => {
  const filtered = media.filter(
    (m) => m.kind === "video" && m.storage === 1 && Number(m.id) < 20,
  );
  const day = groupByCaptureDay(filtered)[0].media;
  assert.deepEqual(
    day.map((m) => m.id),
    ["1", "5", "7", "11", "13", "17", "19"],
  );
  const initial = new Set(["0", "35"]);
  const selected = setMediaSelection(initial, day, true);
  assert.deepEqual(
    [...selected],
    ["0", "35", "1", "5", "7", "11", "13", "17", "19"],
  );
  assert.deepEqual([...setMediaSelection(selected, day, false)], ["0", "35"]);
});

test("单项变化同步半选状态，空范围和未知日期分组可安全选择", () => {
  assert.equal(mediaSelectionState(new Set(), []), false);
  assert.equal(mediaSelectionState(new Set(["other"]), []), false);
  const unknown = groupByCaptureDay([
    { id: "blank", date: "" },
    { id: "invalid", date: "2026-02-30 12:00:00" },
  ])[0];
  assert.equal(unknown.day, "unknown");
  const selected = setMediaSelection(new Set(["other"]), unknown.media, true);
  assert.equal(mediaSelectionState(selected, unknown.media), true);
  selected.delete("blank");
  assert.equal(mediaSelectionState(selected, unknown.media), "indeterminate");
  assert.deepEqual(
    [...setMediaSelection(selected, unknown.media, false)],
    ["other"],
  );
});

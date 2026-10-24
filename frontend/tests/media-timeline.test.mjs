import assert from "node:assert/strict";
import test from "node:test";
import {
  captureDate,
  groupByCaptureDay,
  sortByCaptureDate,
} from "../src/lib/media-timeline.ts";

const item = (id, date) => ({ id, date });

test("日期校验遵循相机日历，不依赖系统时区或宽松日期解析", () => {
  for (const value of [
    "2024-02-29 00:00:00",
    "2026-10-23 23:59:59",
    "2000-02-29 12:30:00",
  ])
    assert.equal(captureDate(value), value);
  for (const value of [
    "",
    "无日期",
    "2026-02-29 12:00:00",
    "1900-02-29 00:00:00",
    "2026-04-31 12:00:00",
    "2026-00-01 00:00:00",
    "2026-13-01 00:00:00",
    "2026-10-00 00:00:00",
    "2026-10-23 24:00:00",
    "2026-10-23 00:60:00",
    "2026-10-23 00:00:60",
    "0000-01-01 00:00:00",
    "2026-10-23T00:00:00Z",
  ])
    assert.equal(captureDate(value), null, value);
});

test("共享时间排序后分页，未知日期置后，同时间稳定，不修改相机原清单", () => {
  const media = Array.from({ length: 30 }, (_, i) =>
    item(String(i), `2026-10-${String(i + 1).padStart(2, "0")} 12:30:00`),
  );
  media.push(
    item("unknown", ""),
    item("invalid", "2026-02-30 12:00:00"),
    item("tie", media[29].date),
  );
  const original = structuredClone(media);
  const sorted = sortByCaptureDate(media);
  assert.deepEqual(
    sorted.slice(0, 2).map((m) => m.id),
    ["29", "tie"],
  );
  assert.equal(sorted[23].id, "7");
  assert.equal(sorted[24].id, "6");
  assert.deepEqual(
    sorted.slice(-2).map((m) => m.id),
    ["unknown", "invalid"],
  );
  assert.deepEqual(media, original);
});

test("按原始拍摄日分组，跨年/跨日排序且无效日期归为同一未知组", () => {
  const groups = groupByCaptureDay(
    sortByCaptureDate([
      item("old", "2025-12-31 23:59:59"),
      item("early", "2026-01-01 00:00:00"),
      item("unknown", ""),
      item("late", "2026-01-01 23:59:59"),
      item("invalid", "2026-02-30 12:00:00"),
    ]),
  );
  assert.deepEqual(
    groups.map((g) => [g.day, g.label, g.media.map((m) => m.id)]),
    [
      ["2026-01-01", "2026年1月1日", ["late", "early"]],
      ["2025-12-31", "2025年12月31日", ["old"]],
      ["unknown", "拍摄时间未知", ["unknown", "invalid"]],
    ],
  );
  assert.deepEqual(groupByCaptureDay([]), []);
});

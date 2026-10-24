import assert from "node:assert/strict";
import test from "node:test";
import { scopedTasks, importSummary } from "../src/lib/device-tasks.ts";

const task = (id, status, total, downloaded = 0, phase = "downloading") => ({
  id,
  status,
  media: { size: total },
  progress: { total, bytes: downloaded, phase },
});

test("本次导入按规划 ID 取范围，不混入历史或其他排队任务", () => {
  const tasks = [
    task("old", "completed", 100, 100),
    task("current", "downloading", 200, 50),
    task("other", "queued", 300),
  ];
  const scope = scopedTasks(tasks, ["current", "missing"]);
  assert.deepEqual(
    scope.map((t) => t.id),
    ["current"],
  );
  assert.equal(importSummary(scope).percent, 25);
  assert.equal(tasks.length, 3);
});
test("下载按字节统计，大文件未完成时不能按文件数量显示高进度", () => {
  const state = importSummary([
    task("small", "completed", 10, 10),
    task("large", "downloading", 990, 90),
  ]);
  assert.equal(state.percent, 10);
  assert.equal(state.completed, 1);
  assert.equal(state.pending, 1);
});
test("生成指纹或最终保存阶段保持未完成，全部成功才显示100%", () => {
  for (const phase of ["fingerprinting", "publishing"]) {
    assert.equal(
      importSummary([task("one", "downloading", 100, 100, phase)]).percent,
      99.9,
    );
  }
  assert.equal(
    importSummary([task("one", "completed", 100, 100)]).percent,
    100,
  );
  assert.equal(importSummary([task("empty", "queued", 0)]).percent, null);
});
test("暂停、部分失败和历史空态分别呈现，不误报成功", () => {
  assert.equal(
    importSummary([task("one", "paused", 100, 80)]).title,
    "导入已暂停",
  );
  const partial = importSummary([
    task("one", "completed", 100, 100),
    task("two", "failed", 100, 20),
  ]);
  assert.equal(partial.title, "导入部分完成");
  assert.equal(partial.failed, 1);
  assert.equal(partial.percent, 60);
  assert.equal(importSummary([task("bad", "failed", 100)]).title, "导入失败");
  assert.equal(importSummary([]).title, "导入任务与记录");
});

// 调用方提供测试工程根目录和 device-fixture.mjs 文本，不读取生产用户数据。
export default async function runDeviceBrowserChecks(
  page,
  { root, fixture, writeReport = async () => {} },
) {
  const checks = [];
  const assert = (v, message) => {
    if (!v) throw Error(message);
    checks.push(message);
  };
  await page.unrouteAll({ behavior: "wait" });
  await page.route("**/src/lib/devices.ts*", (r) =>
    r.fulfill({ contentType: "application/javascript", body: fixture }),
  );
  await page.route("**/src/lib/api.ts*", (r) =>
    r.fulfill({
      contentType: "application/javascript",
      body: `import {emptySummary,idleJob} from '/src/lib/types.ts'; export const isDesktop=true; export const api={summary:async()=>emptySummary,status:async()=>idleJob,history:async()=>[]};`,
    }),
  );
  await page.route("**/__fixture_media_unavailable__", (r) =>
    r.fulfill({ contentType: "video/mp4", body: "" }),
  );
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("http://127.0.0.1:9246/?device-qa=1");
  await page.getByRole("button", { name: "设备导入", exact: true }).click();
  await page.getByRole("button", { name: "连接设备", exact: true }).click();
  await page.getByRole("button", { name: "搜索附近相机", exact: true }).click();
  await page
    .getByRole("button", { name: /DJI Osmo Action 6 测试设备/ })
    .click();
  await page.getByRole("button", { name: /测试相机热点/ }).click();
  await page.getByRole("button", { name: "连接此热点", exact: true }).click();
  assert(
    await page
      .getByText("请输入 8–63 字节的密码", { exact: false })
      .isVisible(),
    "密码校验保留对话框并提示修正",
  );
  assert(
    await page.evaluate(
      () => !window.__deviceFixture.calls.includes("join_wifi"),
    ),
    "无效密码不会调用连接接口",
  );
  await page.getByLabel("相机 Wi-Fi 密码", { exact: true }).fill("TestPass42");
  await page.getByRole("button", { name: "显示密码", exact: true }).click();
  assert(
    (await page
      .getByLabel("相机 Wi-Fi 密码", { exact: true })
      .getAttribute("type")) === "text",
    "密码显隐可操作",
  );
  await page.getByRole("button", { name: "隐藏密码", exact: true }).click();
  await page.getByRole("button", { name: "连接此热点", exact: true }).click();
  await page.getByRole("heading", { name: "设备素材", exact: true }).waitFor();
  const views = page.getByRole("group", { name: "素材展示方式" });
  const types = page.getByRole("group", { name: "设备素材类型" });
  const storages = page.getByRole("group", { name: "设备存储位置" });
  const chosen = async () =>
    Number(
      (
        await page.locator(".device-selection-total strong").innerText()
      ).replace(/[^0-9]/g, ""),
    );
  assert(
    (await page.locator(".device-media-card").count()) === 30,
    "默认网格模式，每页30项",
  );
  assert(
    (await storages
      .getByRole("button", { name: "机身存储", exact: true })
      .isVisible()) &&
      (await storages
        .getByRole("button", { name: "存储卡", exact: true })
        .isVisible()),
    "机身、存储卡、全部筛选直接可见",
  );
  await page
    .getByRole("checkbox", { name: "选择 DJI_0001.MP4", exact: true })
    .check();
  await views.getByRole("button", { name: "列表", exact: true }).click();
  assert(
    (await page.locator(".device-table tbody tr").count()) === 30,
    "切换列表视图",
  );
  assert(
    await page
      .getByRole("checkbox", { name: "选择 DJI_0001.MP4", exact: true })
      .isChecked(),
    "列表与网格共用选择",
  );
  await page.getByRole("button", { name: "清空全部选择", exact: true }).click();
  await views.getByRole("button", { name: "时间线", exact: true }).click();
  const day23 = () =>
    page.getByRole("checkbox", {
      name: "选择2026年10月23日的全部素材",
      exact: true,
    });
  await day23().check();
  assert((await chosen()) === 32, "按天选择包含当前筛选的全部分页，共32项");
  await page
    .getByRole("button", { name: "设备素材下一页", exact: true })
    .click();
  assert(await day23().isChecked(), "时间线翻页后整天选择状态保持");
  await page
    .getByRole("checkbox", { name: "选择 DJI_0061.MP4", exact: true })
    .uncheck();
  assert(
    (await day23().getAttribute("aria-checked")) === "mixed",
    "单项取消同步日期半选状态",
  );
  await page
    .getByRole("checkbox", { name: "选择 DJI_0000.JPG", exact: true })
    .check();
  await day23().check();
  await day23().uncheck();
  assert((await chosen()) === 1, "取消当天只影响该日，保留其他日期已选素材");
  await page.getByRole("button", { name: "清空全部选择", exact: true }).click();
  await types.getByRole("button", { name: "视频", exact: true }).click();
  await storages.getByRole("button", { name: "机身存储", exact: true }).click();
  await day23().check();
  assert((await chosen()) === 11, "日期批选同时遵循视频与机身存储筛选");
  await views.getByRole("button", { name: "网格", exact: true }).click();
  assert(
    (await page.locator(".device-media-card").count()) === 11 &&
      (await chosen()) === 11,
    "网格展示筛选结果，切换不清空选择",
  );
  await page
    .getByRole("button", { name: "预览 DJI_0003.MP4", exact: true })
    .click();
  assert(await page.getByRole("dialog").isVisible(), "网格封面可打开素材预览");
  await page.keyboard.press("Escape");
  assert(
    await page
      .getByRole("button", { name: "预览 DJI_0003.MP4", exact: true })
      .evaluate((el) => el === document.activeElement),
    "预览关闭后焦点返回素材封面",
  );
  await page
    .getByRole("textbox", { name: "搜索设备素材" })
    .fill("没有这样的照片");
  await page.getByText("没有符合筛选条件的素材", { exact: true }).waitFor();
  await page.getByRole("button", { name: "清空设备素材搜索" }).click();
  await page.locator(".device-media-card").first().waitFor();
  assert(
    (await page.getByRole("textbox", { name: "搜索设备素材" }).inputValue()) ===
      "",
    "搜索清空即时生效",
  );
  await page.getByLabel("开始日期", { exact: true }).fill("2026-10-24");
  await page.getByLabel("结束日期", { exact: true }).fill("2026-10-20");
  assert(
    await page
      .getByText("开始日期不能晚于结束日期，请调整日期范围。", { exact: true })
      .isVisible(),
    "日期范围错误明确展示",
  );
  await page.getByRole("button", { name: "重置筛选", exact: true }).click();
  await page.getByRole("button", { name: "清空全部选择", exact: true }).click();
  await views.getByRole("button", { name: "时间线", exact: true }).click();
  await day23().check();
  await page.evaluate(() => {
    const f = window.__deviceFixture;
    f.state.tasks.push({
      id: "old-task",
      providerId: "dji-action6",
      deviceId: "old-camera",
      deviceName: "历史设备",
      media: structuredClone(f.media[0]),
      root: "/测试/旧资料库",
      relativePath: "OLD.JPG",
      layout: "date",
      status: "completed",
      progress: { bytes: 1024, total: 1024, speed: 0, phase: "completed" },
      error: "",
      warning: "",
      path: "/测试/旧资料库/OLD.JPG",
      sha256: "a".repeat(64),
      createdAt: 1,
    });
  });
  await page.getByRole("button", { name: "选择设备素材导入文件夹" }).click();
  await page.getByRole("combobox", { name: "目录组织方式" }).click();
  await page.keyboard.press("Escape");
  assert(
    await page
      .getByRole("combobox", { name: "目录组织方式" })
      .evaluate((el) => el === document.activeElement),
    "组织方式下拉菜单 Escape 归还焦点",
  );
  await page.getByRole("button", { name: "预览导入", exact: true }).click();
  assert(
    await page
      .getByRole("heading", { name: "确认导入 32 项原始素材" })
      .isVisible(),
    "导入确认包含跨页当天32项素材",
  );
  assert(
    (await page.getByRole("dialog").innerText()).includes("/测试/影像资料库"),
    "导入前展示明确目标目录",
  );
  await page.getByRole("button", { name: "开始导入", exact: true }).click();
  await page
    .getByRole("heading", { name: "正在导入原始素材", exact: true })
    .waitFor();
  const dialog = () => page.getByRole("dialog");
  assert(
    (await dialog()
      .getByRole("button", { name: "本次导入 · 32", exact: true })
      .getAttribute("aria-pressed")) === "true",
    "开始导入自动弹出本次任务",
  );
  assert(
    (await page.locator(".device-task-record").count()) === 10,
    "弹窗任务记录每页10项",
  );
  assert(
    (await dialog().locator(".task-counts").innerText()).includes("已完成\n0"),
    "本次进度不混入历史已完成任务",
  );
  await page.screenshot({
    path: `${root}/work/device-task-dialog-running.png`,
    fullPage: false,
  });
  await dialog().getByRole("button", { name: "后台继续", exact: true }).click();
  assert(
    (await page.getByRole("dialog").count()) === 0,
    "关闭任务弹窗不取消传输",
  );
  assert(
    (await page.locator(".device-task-panel").count()) === 0 &&
      (await page.locator(".device-task-record").count()) === 0,
    "页面底部不再展示导入记录",
  );
  assert(
    await page
      .getByRole("button", { name: "导入任务", exact: true })
      .evaluate((el) => el === document.activeElement),
    "任务弹窗关闭后焦点回到任务入口",
  );
  await page.getByRole("button", { name: "导入任务", exact: true }).click();
  await dialog().getByRole("button", { name: "暂停全部", exact: true }).click();
  await dialog()
    .getByRole("heading", { name: "导入已暂停", exact: true })
    .waitFor();
  await dialog()
    .getByRole("button", { name: "继续导入", exact: true })
    .first()
    .click();
  await dialog()
    .getByRole("button", { name: "查看文件", exact: true })
    .first()
    .waitFor();
  assert(
    await page.getByText("已导入", { exact: true }).first().isVisible(),
    "暂停后单项恢复并展示结果",
  );
  await dialog()
    .getByRole("button", { name: "继续未完成任务", exact: true })
    .click();
  await dialog()
    .getByRole("heading", { name: "导入完成", exact: true })
    .waitFor();
  assert(
    await dialog().getByText("100.0%", { exact: true }).isVisible(),
    "所有本次任务完成后总进度才显示100%",
  );
  await dialog()
    .getByRole("button", { name: "查看 DJI_0001.MP4 导入详情", exact: true })
    .click();
  assert(
    await dialog().getByText("本地 SHA-256", { exact: true }).isVisible(),
    "详情在任务弹窗内展开，不叠加第二层弹窗",
  );
  await dialog()
    .getByRole("button", { name: "全部记录 · 33", exact: true })
    .click();
  await dialog()
    .getByRole("button", { name: "导入记录下一页", exact: true })
    .click();
  await dialog()
    .getByRole("button", { name: "导入记录下一页", exact: true })
    .click();
  await dialog()
    .getByRole("button", { name: "导入记录下一页", exact: true })
    .click();
  assert(
    await dialog().getByText("历史设备", { exact: false }).isVisible(),
    "全部记录保留历史设备导入",
  );
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForFunction(() => {
    const r = document.querySelector("[role=dialog]").getBoundingClientRect();
    return (
      r.left >= 0 &&
      r.right <= innerWidth &&
      r.top >= 0 &&
      r.bottom <= innerHeight
    );
  });
  assert(
    await dialog().evaluate((el) => {
      const r = el.getBoundingClientRect();
      return (
        r.left >= 0 &&
        r.right <= innerWidth &&
        r.top >= 0 &&
        r.bottom <= innerHeight
      );
    }),
    "390px任务弹窗及操作栏位于视口内",
  );
  await page.screenshot({
    path: `${root}/work/device-task-dialog-mobile.png`,
    fullPage: false,
  });
  await dialog()
    .locator("[data-slot=dialog-footer]")
    .getByRole("button", { name: "关闭", exact: true })
    .click();
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
    "390px素材页面无横向溢出",
  );
  await page.screenshot({
    path: `${root}/work/device-timeline-mobile.png`,
    fullPage: true,
  });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.screenshot({
    path: `${root}/work/device-timeline-desktop.png`,
    fullPage: true,
  });
  await views.getByRole("button", { name: "网格", exact: true }).click();
  await page.screenshot({
    path: `${root}/work/device-grid-desktop.png`,
    fullPage: true,
  });
  await page.evaluate(() => (window.__deviceFixture.failRefresh = true));
  await page.getByRole("button", { name: "刷新素材", exact: true }).click();
  await page
    .getByText("测试：SD 卡读取失败，已保留原素材清单", { exact: true })
    .waitFor();
  assert(
    (await page.locator(".device-media-card").count()) === 30,
    "刷新失败保留原有素材和视图",
  );
  await page.getByRole("button", { name: "断开", exact: true }).click();
  assert(
    await page.getByRole("dialog").isVisible(),
    "断开设备说明暂停与网络恢复后果",
  );
  await page.getByRole("button", { name: "保持连接", exact: true }).click();
  await page.getByRole("button", { name: "素材分析", exact: true }).click();
  await page
    .getByRole("heading", { name: "从一张存储卡，到井然有序。", exact: true })
    .waitFor();
  await page.getByRole("button", { name: "设备导入", exact: true }).click();
  assert(
    (await views
      .getByRole("button", { name: "网格", exact: true })
      .getAttribute("aria-pressed")) === "true",
    "切换兄弟页面保留素材展示方式",
  );
  await page.getByRole("button", { name: "导入任务", exact: true }).click();
  assert(
    await dialog()
      .getByRole("button", { name: "全部记录 · 33", exact: true })
      .isVisible(),
    "任务弹窗可随时重新打开查看记录",
  );
  await dialog()
    .locator("[data-slot=dialog-footer]")
    .getByRole("button", { name: "关闭", exact: true })
    .click();
  await writeReport({
    checks,
    source: "浏览器模拟接口，仅验证 UI；不代表实机连接或真实文件传输。",
  });
  return checks;
}

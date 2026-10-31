// 用回环开发服务器和可控接口替身验证界面，不读取生产日志或导出真实文件。
export default async function runDiagnosticsChecks(
  page,
  { fixture, deviceFixture, output },
) {
  const checks = [];
  await page.evaluate(() => (window.__diagnosticsChecks = []));
  const assert = (value, label) => {
    if (!value) throw Error(label);
    checks.push(label);
  };
  const ready = async () => {
    await page.waitForFunction(
      () =>
        !document
          .querySelector(".diagnostic-scroll")
          ?.getAttribute("aria-busy") ||
        document
          .querySelector(".diagnostic-scroll")
          ?.getAttribute("aria-busy") === "false",
    );
  };
  await page.unrouteAll({ behavior: "wait" });
  await page.route("**/src/lib/diagnostics.ts*", (r) =>
    r.fulfill({ contentType: "application/javascript", body: fixture }),
  );
  await page.route("**/src/lib/devices.ts*", (r) =>
    r.fulfill({ contentType: "application/javascript", body: deviceFixture }),
  );
  await page.route("**/src/lib/api.ts*", (r) =>
    r.fulfill({
      contentType: "application/javascript",
      body: `import {emptySummary,idleJob} from '/src/lib/types.ts'; export const isDesktop=true; export const api={summary:async()=>emptySummary,status:async()=>idleJob,history:async()=>[]};`,
    }),
  );
  await page.setViewportSize({ width: 1440, height: 1100 });
  await page.goto("http://127.0.0.1:9246/");
  await page.getByRole("button", { name: "诊断日志", exact: true }).click();
  await page.getByText("本地自动记录已开启", { exact: true }).waitFor();
  assert(
    (await page.locator(".diagnostic-entry").count()) === 50,
    "首次读取50条本地日志",
  );
  await page.getByRole("button", { name: "加载更早记录" }).click();
  await page.waitForFunction(
    () => document.querySelectorAll(".diagnostic-entry").length === 65,
  );
  assert(
    (await page.locator(".diagnostic-entry").count()) === 65,
    "游标分页加载更早记录",
  );
  await page.locator(".diagnostic-entry").first().locator("summary").focus();
  await page.keyboard.press("Enter");
  assert(
    (await page
      .locator(".diagnostic-entry")
      .first()
      .locator("details")
      .getAttribute("open")) !== null,
    "键盘可展开详细字段与精确时间",
  );
  await page.getByRole("combobox", { name: "筛选级别" }).click();
  await page.getByRole("option", { name: "错误", exact: true }).click();
  await page.waitForFunction(
    () => document.querySelectorAll(".diagnostic-entry").length === 17,
  );
  assert(
    await page
      .locator(".diagnostic-entry .diagnostic-level")
      .evaluateAll((nodes) => nodes.every((n) => n.textContent === "错误")),
    "级别筛选由查询接口完成",
  );
  await page.getByRole("combobox", { name: "筛选级别" }).click();
  await page.getByRole("option", { name: "全部级别", exact: true }).click();
  await page.waitForFunction(
    () => document.querySelectorAll(".diagnostic-entry").length === 50,
  );
  const search = page.getByRole("textbox", { name: "搜索诊断日志" });
  await search.fill("旧查询");
  await page.waitForFunction(() =>
    window.__diagnosticsFixture.calls.some((c) => c.search === "旧查询"),
  );
  await search.fill("新查询");
  await page.getByText("测试：新查询连接记录", { exact: true }).waitFor();
  await page.waitForTimeout(1200);
  assert(
    (await page.locator(".diagnostic-entry").count()) === 1 &&
      (await page
        .locator(".diagnostic-entry")
        .innerText()
        .then((v) => v.includes("新查询"))),
    "过期搜索响应不覆盖较新结果",
  );
  await page.getByRole("button", { name: "清空日志搜索" }).click();
  await page.waitForFunction(
    () => document.querySelectorAll(".diagnostic-entry").length === 50,
  );
  assert(
    await search.evaluate((node) => node === document.activeElement),
    "清空搜索后焦点回到输入框",
  );
  const reads = await page.evaluate(
    () =>
      window.__diagnosticsFixture.calls.filter((c) => c.type === "read").length,
  );
  await search.dispatchEvent("compositionstart");
  await search.fill("输入法");
  await page.waitForTimeout(400);
  assert(
    (await page.evaluate(
      () =>
        window.__diagnosticsFixture.calls.filter((c) => c.type === "read")
          .length,
    )) === reads,
    "中文输入法组合期间不发查询",
  );
  await search.dispatchEvent("compositionend");
  await page.getByText("没有匹配的记录", { exact: true }).waitFor();
  await page.getByRole("button", { name: "清除筛选", exact: true }).click();
  await page.waitForFunction(
    () => document.querySelectorAll(".diagnostic-entry").length === 50,
  );
  await page.evaluate(() => (window.__diagnosticsFixture.failRead = true));
  await page.getByRole("button", { name: "刷新日志" }).click();
  await page.getByText("测试：日志读取失败，请重试", { exact: true }).waitFor();
  assert(
    (await page.locator(".diagnostic-entry").count()) === 50,
    "读取失败保留已有日志",
  );
  await page.getByRole("button", { name: "重新读取", exact: true }).click();
  await ready();
  await page.getByRole("combobox", { name: "记录详细程度" }).click();
  await page.getByRole("option", { name: "详细记录", exact: true }).click();
  await page
    .getByText("已开启详细记录，后续操作会记录更多连接过程。", { exact: true })
    .waitFor();
  assert(
    (await page.evaluate(
      () => window.__diagnosticsFixture.info.settings.level,
    )) === "debug",
    "切换详细记录后立即生效",
  );
  await page.evaluate(() => (window.__diagnosticsFixture.delayExport = 250));
  await page.getByRole("button", { name: "导出诊断包", exact: true }).click();
  assert(
    await page
      .getByRole("button", { name: "导出诊断包", exact: true })
      .isDisabled(),
    "导出过程中禁止重复提交",
  );
  await page.getByText("/测试/CardAtlas-诊断.zip", { exact: true }).waitFor();
  await page.evaluate(() => {
    window.__diagnosticsFixture.failExport = true;
    window.__diagnosticsFixture.delayExport = 0;
  });
  await page.getByRole("button", { name: "导出诊断包", exact: true }).click();
  await page.getByText("测试：目标文件夹不可写", { exact: true }).waitFor();
  assert(
    await page
      .getByRole("button", { name: "导出诊断包", exact: true })
      .isEnabled(),
    "导出失败可重试",
  );
  await page.evaluate(() => {
    window.__diagnosticsFixture.failExport = false;
    window.__diagnosticsFixture.cancelExport = true;
  });
  await page.getByRole("button", { name: "导出诊断包", exact: true }).click();
  await page
    .getByText("已取消导出，日志仍保存在本机。", { exact: true })
    .waitFor();
  await page.getByRole("button", { name: "打开日志文件夹" }).click();
  assert(
    await page.evaluate(() =>
      window.__diagnosticsFixture.calls.some((c) => c.type === "directory"),
    ),
    "日志文件夹入口可用",
  );
  await page.context().setOffline(true);
  await page.getByRole("button", { name: "刷新日志" }).click();
  await ready();
  assert(
    (await page.locator(".diagnostic-entry").count()) === 50,
    "断网状态仍可调用本地日志接口",
  );
  await page.context().setOffline(false);
  await page.screenshot({
    path: output + "/diagnostics-desktop.png",
    fullPage: true,
  });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator(".diagnostic-entry").first().locator("summary").click();
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth + 1,
    ),
    "窄屏及长错误详情无页面横向溢出",
  );
  await page.getByRole("combobox", { name: "记录详细程度" }).click();
  await page.getByRole("option", { name: "详细记录", exact: true }).waitFor();
  assert(
    await page.getByRole("listbox").evaluate((node) => {
      const r = node.getBoundingClientRect();
      return (
        r.x >= 0 &&
        r.right <= window.innerWidth &&
        r.bottom <= window.innerHeight
      );
    }),
    "窄屏下拉选项保持在视口内",
  );
  await page.keyboard.press("Escape");
  await page.screenshot({
    path: output + "/diagnostics-mobile.png",
    fullPage: true,
  });
  await page.setViewportSize({ width: 1440, height: 1100 });
  await page.getByRole("button", { name: "素材分析", exact: true }).click();
  await page.getByRole("button", { name: "诊断日志", exact: true }).click();
  await ready();
  assert(
    (await page.getByRole("combobox", { name: "记录详细程度" }).innerText()) ===
      "详细记录",
    "导航切换后仍显示实际记录设置",
  );
  return checks;
}

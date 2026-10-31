// 只通过模块和素材请求拦截注入离线数据，不连接相机，不执行导入。
export default async function runPreviewBrowserChecks(page, { root, fixture }) {
  const checks = [];
  const assert = (condition, label) => {
    if (!condition) throw Error(label);
    checks.push(label);
  };
  const pageErrors = [];
  const onPageError = (error) => pageErrors.push(error.message);
  page.on("pageerror", onPageError);
  await page.unrouteAll({ behavior: "wait" });
  await page.goto("about:blank");
  // 从画布生成测试视频，不申请摄像头权限，不读取用户媒体。
  const videoBase64 = await page.evaluate(async () => {
    const canvas = document.createElement("canvas");
    canvas.width = 320;
    canvas.height = 180;
    const context = canvas.getContext("2d");
    const stream = canvas.captureStream(12);
    const recorder = new MediaRecorder(stream, {
      mimeType: "video/webm;codecs=vp8",
    });
    const chunks = [];
    recorder.ondataavailable = (event) => chunks.push(event.data);
    const stopped = new Promise((resolve) => (recorder.onstop = resolve));
    recorder.start();
    for (let frame = 0; frame < 12; frame++) {
      context.fillStyle = frame % 2 ? "#28634c" : "#3b7058";
      context.fillRect(0, 0, 320, 180);
      context.fillStyle = "#e8d9a5";
      context.fillRect(20 + frame * 10, 45, 90, 90);
      await new Promise((resolve) => setTimeout(resolve, 70));
    }
    recorder.stop();
    await stopped;
    stream.getTracks().forEach((track) => track.stop());
    const data = new Uint8Array(await new Blob(chunks).arrayBuffer());
    return btoa(String.fromCharCode(...data));
  });
  const videoBytes = Buffer.from(videoBase64, "base64");
  const control = {
    broken: true,
    rawGate: null,
    brokenGate: null,
    slowGate: null,
  };
  let releaseRaw;
  control.rawGate = new Promise((resolve) => (releaseRaw = resolve));
  let releaseSlow;
  control.slowGate = new Promise((resolve) => (releaseSlow = resolve));
  const svg = (width, height) =>
    `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}"><defs><linearGradient id="g" x2="1" y2="1"><stop stop-color="#3b7058"/><stop offset="1" stop-color="#aeb968"/></linearGradient></defs><rect width="100%" height="100%" fill="url(#g)"/><path d="M0 ${height * 0.8} Q${width * 0.3} ${height * 0.1} ${width} ${height * 0.5} V${height} H0Z" fill="#1d4b3b"/><circle cx="${width * 0.74}" cy="${height * 0.27}" r="${width * 0.12}" fill="#e8d9a5"/><text x="${width * 0.08}" y="${height * 0.9}" fill="white" font-size="${width * 0.08}" font-family="sans-serif">离线预览测试</text></svg>`;
  const media = [
    {
      id: "raw",
      name: "DSC00052.ARW",
      path: "存储卡/2026-10-31/DSC00052.ARW",
      kind: "photo",
      size: 25438453,
    },
    {
      id: "portrait",
      name: "PORTRAIT.JPG",
      path: "DCIM/100MSDCF/PORTRAIT.JPG",
      kind: "photo",
      size: 6291456,
    },
    {
      id: "video",
      name: "C0226.MP4",
      path: "DCIM/100MSDCF/C0226.MP4",
      kind: "video",
      size: 96479477,
    },
    {
      id: "broken",
      name: "BROKEN.MP4",
      path: "DCIM/100MSDCF/BROKEN.MP4",
      kind: "video",
      size: 1048576,
    },
    {
      id: "slow",
      name: "SLOW.JPG",
      path: "DCIM/100MSDCF/SLOW.JPG",
      kind: "photo",
      size: 1048576,
    },
    {
      id: "long",
      name: "非常长的摄影素材名称用于离线验证弹窗标题和文件路径的换行行为与可访问详情展示.ARW",
      path: "DCIM/很长的目录名称/".repeat(6) + "非常长的摄影素材名称.ARW",
      kind: "photo",
      size: 1048576,
    },
  ].map((file) => ({
    ...file,
    storageId: "0",
    storageName: "存储卡",
    date: "2026-10-31 09:37:04",
    sizeEstimated: false,
    dateSource: "相机媒体服务",
    thumbnail: true,
    duration: file.kind === "video" ? 7 : 0,
  }));
  const marker =
    'export const deviceMediaURL = () => "/__fixture_media_unavailable__";';
  assert(fixture.includes(marker), "离线素材模块与工程接口一致");
  fixture = fixture.replace(
    marker,
    'export const deviceMediaURL = (provider,id,preview,session) => `/__preview_fixture__/${id}/${preview ? "preview" : "thumb"}?session=${session}`;',
  );
  fixture += `\nmedia.splice(0,media.length,...${JSON.stringify(media)});fixture.state.activeProvider='sony-zve10';Object.assign(fixture.state.connection,{phase:'connected',sessionToken:'offline-preview-qa',deviceName:'Sony ZV-E10',revision:1});`;
  await page.route("**/src/lib/devices.ts*", (route) =>
    route.fulfill({ contentType: "application/javascript", body: fixture }),
  );
  await page.route("**/src/lib/api.ts*", (route) =>
    route.fulfill({
      contentType: "application/javascript",
      body: "import {emptySummary,idleJob} from '/src/lib/types.ts';export const isDesktop=true;export const api={summary:async()=>emptySummary,status:async()=>idleJob,history:async()=>[]};",
    }),
  );
  await page.route("**/__preview_fixture__/**", async (route) => {
    const segments = new URL(route.request().url()).pathname.split("/");
    const id = segments[2];
    const preview = segments[3] === "preview";
    if (id === "raw" && control.rawGate) await control.rawGate;
    if (id === "slow" && preview && control.slowGate) await control.slowGate;
    if (id === "broken" && preview && control.brokenGate)
      await control.brokenGate;
    try {
      if (id === "broken" && preview && control.broken)
        return await route.fulfill({ status: 503, body: "离线模拟读取失败" });
      if ((id === "video" || id === "broken") && preview)
        return await route.fulfill({
          contentType: "video/webm",
          body: videoBytes,
          headers: { "Cache-Control": "no-store" },
        });
      await route.fulfill({
        contentType: "image/svg+xml",
        body: svg(
          id === "portrait" ? 900 : 160,
          id === "portrait" ? 1350 : 120,
        ),
        headers: { "Cache-Control": "no-store" },
      });
    } catch {
      // 关闭弹窗可能中止尚未完成的测试请求。
    }
  });
  let openedName = "";
  let releaseBroken;
  const open = async (name) => {
    openedName = name;
    await page
      .getByRole("button", { name: `预览 ${name}`, exact: true })
      .click();
    await page.getByRole("dialog").waitFor();
  };
  const stage = () => page.locator(".device-preview-stage");
  const ready = () =>
    page.locator('.device-preview-stage[data-state="ready"]').waitFor();
  const close = async () => {
    await page.keyboard.press("Escape");
    await page.getByRole("dialog").waitFor({ state: "hidden" });
    await page.waitForFunction(
      (name) =>
        document.activeElement?.getAttribute("aria-label") === `预览 ${name}`,
      openedName,
    );
  };
  const sameBox = (a, b) =>
    ["x", "y", "width", "height"].every(
      (key) => Math.abs(a[key] - b[key]) <= 1,
    );
  try {
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto("http://127.0.0.1:9246/?preview-qa=1");
    await page.getByRole("button", { name: "设备导入", exact: true }).click();
    await page
      .getByRole("heading", { name: "设备素材", exact: true })
      .waitFor();
    await open("DSC00052.ARW");
    await page.getByText("正在读取相机缩略图", { exact: true }).waitFor();
    const loadingBox = await stage().boundingBox();
    const loadingDialog = await page.getByRole("dialog").boundingBox();
    assert(
      await page
        .getByRole("button", { name: "关闭", exact: true })
        .evaluate((node) => node === document.activeElement),
      "打开预览聚焦关闭按钮，避免整框焦点边线",
    );
    releaseRaw();
    control.rawGate = null;
    await ready();
    assert(
      sameBox(loadingBox, await stage().boundingBox()) &&
        sameBox(loadingDialog, await page.getByRole("dialog").boundingBox()),
      "图片加载前后画布和弹窗不跳动",
    );
    const image = page.getByRole("img", {
      name: "ARW 相机缩略图",
      exact: true,
    });
    assert(
      await image.evaluate(
        (node) =>
          node.naturalWidth === 160 && node.getBoundingClientRect().width > 320,
      ),
      "160 像素缩略图默认放大适应画布",
    );
    assert(
      await page.getByText("160 × 120", { exact: true }).isVisible(),
      "缩略图真实尺寸明确可见",
    );
    await page.screenshot({
      path: root + "/work/device-preview-raw-desktop.png",
    });
    await page.getByRole("button", { name: "实际尺寸", exact: true }).click();
    await page.waitForFunction(
      () =>
        document
          .querySelector(".device-preview-stage")
          ?.getAttribute("data-sizing") === "actual" &&
        document.querySelector(".device-preview-image")?.clientWidth === 160,
    );
    assert(
      Math.abs((await image.boundingBox()).width - 160) < 1,
      "实际尺寸显示保持 1:1",
    );
    await page.getByRole("button", { name: "适应画布", exact: true }).click();
    await close();
    assert(
      await page
        .getByRole("button", { name: "预览 DSC00052.ARW", exact: true })
        .evaluate((node) => node === document.activeElement),
      "Escape 关闭后焦点回到原素材",
    );
    await open("PORTRAIT.JPG");
    await ready();
    assert(
      await page
        .getByRole("img", { name: "PORTRAIT.JPG", exact: true })
        .evaluate((node) => getComputedStyle(node).objectFit === "contain"),
      "竖图完整保留比例",
    );
    await page.getByRole("button", { name: "实际尺寸", exact: true }).click();
    assert(
      await stage().evaluate((node) => node.scrollHeight >= 1350),
      "大图 1:1 模式可滚动查看",
    );
    await page.keyboard.press("Tab");
    assert(
      await stage().evaluate((node) => node === document.activeElement),
      "实际尺寸画布支持键盘聚焦",
    );
    await page.keyboard.press("ArrowDown");
    await page.waitForFunction(
      () => document.querySelector(".device-preview-stage").scrollTop > 0,
    );
    await close();
    control.brokenGate = new Promise((resolve) => (releaseBroken = resolve));
    await open("BROKEN.MP4");
    await page.getByText("正在读取视频", { exact: true }).waitFor();
    const videoLoading = await stage().boundingBox();
    assert(
      await page
        .getByRole("button", { name: "播放视频", exact: true })
        .isVisible(),
      "视频信息未加载时仍提供用户手势播放入口",
    );
    await page.getByRole("button", { name: "播放视频", exact: true }).click();
    assert(
      (await page.locator("video").boundingBox()).width >=
        videoLoading.width - 1,
      "视频加载前占满预定画布，不显示 300×150 小播放器",
    );
    releaseBroken();
    control.brokenGate = null;
    await page.getByText("暂时无法播放视频", { exact: true }).waitFor();
    assert(
      sameBox(videoLoading, await stage().boundingBox()),
      "视频失败保持画布几何",
    );
    await page.screenshot({
      path: root + "/work/device-preview-error-desktop.png",
    });
    control.broken = false;
    await page.getByRole("button", { name: "重新加载", exact: true }).click();
    await ready();
    assert(
      sameBox(videoLoading, await stage().boundingBox()),
      "重试成功保持画布几何",
    );
    const video = page.locator("video");
    await video.focus();
    await page.keyboard.press("Space");
    await page.waitForFunction(
      () => document.querySelector("video")?.currentTime > 0.05,
    );
    await video.evaluate((node) => node.dispatchEvent(new Event("waiting")));
    await page.getByText("正在缓冲视频…", { exact: true }).waitFor();
    await video.evaluate((node) => node.pause());
    await page
      .getByText("正在缓冲视频…", { exact: true })
      .waitFor({ state: "hidden" });
    assert(true, "暂停播放会清除缓冲状态");
    await page.screenshot({
      path: root + "/work/device-preview-video-desktop.png",
    });
    await page.evaluate(() => {
      window.__oldPreviewVideo = document.querySelector("video");
      window.__deviceFixture.state.connection.sessionToken =
        "offline-preview-new";
    });
    await page.getByRole("dialog").waitFor({ state: "hidden" });
    assert(
      await page.evaluate(
        () =>
          window.__oldPreviewVideo.paused &&
          !window.__oldPreviewVideo.hasAttribute("src"),
      ),
      "会话切换关闭旧预览，停止播放器并释放旧地址",
    );
    await open("BROKEN.MP4");
    await ready();
    await page.evaluate(() => {
      window.__oldPreviewVideo = document.querySelector("video");
      window.__deviceFixture.state.connection.phase = "lost";
    });
    await page.getByText("设备连接已中断", { exact: true }).waitFor();
    assert(
      (await page.locator("video").count()) === 0,
      "断开连接立即停止视频读取",
    );
    assert(
      await page
        .getByRole("button", { name: "返回素材列表", exact: true })
        .isVisible(),
      "断线提供清晰的返回路径",
    );
    await page
      .getByRole("button", { name: "返回素材列表", exact: true })
      .click();
    await page.evaluate(() => {
      window.__deviceFixture.state.connection.phase = "connected";
    });
    await page.getByText("已连接", { exact: true }).waitFor();
    await open("SLOW.JPG");
    await page
      .getByText("读取时间较长", { exact: true })
      .waitFor({ timeout: 25_000 });
    assert(
      await page
        .getByRole("button", { name: "重新加载", exact: true })
        .isVisible(),
      "超时结束等待并提供重试",
    );
    await close();
    releaseSlow();
    control.slowGate = null;
    await open("DSC00052.ARW");
    await ready();
    assert(
      await page.getByText("160 × 120", { exact: true }).isVisible(),
      "过期图片响应不覆盖新预览",
    );
    await close();
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.setViewportSize({ width: 390, height: 844 });
    await open(media.find((file) => file.id === "long").name);
    await ready();
    let bounds = await page.getByRole("dialog").boundingBox();
    assert(
      bounds.x >= 0 &&
        bounds.y >= 0 &&
        bounds.x + bounds.width <= 390 &&
        bounds.y + bounds.height <= 844,
      "390px 长标题弹窗不越界",
    );
    await page.getByText("源文件位置", { exact: true }).click();
    assert(
      await page.locator(".device-preview-location p").isVisible(),
      "完整长路径可展开查看",
    );
    assert(
      await page
        .locator(".device-preview-layout")
        .evaluate((node) => node.scrollWidth <= node.clientWidth + 1),
      "窄屏信息区没有水平溢出",
    );
    await page.screenshot({ path: root + "/work/device-preview-mobile.png" });
    await close();
    await page.setViewportSize({ width: 900, height: 650 });
    await open("DSC00052.ARW");
    await ready();
    bounds = await page.getByRole("dialog").boundingBox();
    assert(
      bounds.y >= 0 && bounds.y + bounds.height <= 650,
      "矮窗口中弹窗可达且不被裁切",
    );
    assert(
      await page.getByRole("button", { name: "关闭", exact: true }).isVisible(),
      "矮窗口关闭操作始终可见",
    );
    await close();
    assert(
      await page.evaluate(() => window.__deviceFixture.calls.length === 0),
      "离线验收没有扫描、连接或导入调用",
    );
    assert(pageErrors.length === 0, "浏览器无未处理脚本错误");
    return {
      passed: checks.length,
      checks,
      pageErrors,
      screenshots: [
        "device-preview-raw-desktop.png",
        "device-preview-error-desktop.png",
        "device-preview-video-desktop.png",
        "device-preview-mobile.png",
      ],
    };
  } finally {
    releaseRaw?.();
    releaseSlow?.();
    releaseBroken?.();
    page.off("pageerror", onPageError);
  }
}

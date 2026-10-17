# 当前版本验证

## 2026-10-17 品牌图标与 Dock 接入

- 内置 ImageGen 生成品牌图标；Go 标准库统一导出 1024px 主图、256px 侧栏图、32px favicon 与 180px 收藏图，均保留透明通道。
- `common:generate:icons` 成功生成多尺寸 ICNS/ICO；再次运行品牌生成任务显示 up to date，未造成重复热更新。
- 正式 `package` 和 `build DEV=true` 都通过。调试 `.app` 已实际启动，原生资源服务器已加载 `/cardatlas.png`，工作台正常显示。
- 两种 `.app` 均通过 codesign 严格验证，引用正确的 `icons.icns`，资源文件 SHA-256 与生成物一致，且均没有遗留的 `Assets.car`。
- `LSUIElement=false`，运行时明确使用 Regular 激活策略，并以内嵌 PNG 设置 Wails `Options.Icon`。
- macOS Plist 校验、前端严格静态审计、类型检查、格式检查、Go 竞态测试和 go vet 通过。侧栏 Logo 已在真实浏览器中确认加载为 256px 图源、48px 显示框，页面无横向溢出。品牌设计和完整生成提示词见 `assets/brand/README.md`。
- 正式应用已更新到 `bin/cardatlas.app`；更新前的应用保留在 `work/previous-build-before-logo`，未强制结束用户的旧实例。

验证时间：2026-10-14，macOS Apple Silicon，Go 1.27.1，Node 24.13.0。

- `go test -race ./...`：通过。覆盖递归扫描、忽略文件/链接、日期闭区间、目录预览、同名文件不覆盖、内容与源文件保留、源文件变化、失效选择、嵌套输出限制、取消清理、符号链接导出范围、MP4 32/64 位时间、损坏元数据和 TIFF EXIF 日期。
- `npm run typecheck` / `npm run build`：通过；生产资源由 Wails 嵌入。
- Wails beta.29 `package`：生成 `bin/cardatlas.app`，本地签名。
- 原生窗口：使用隔离测试目录扫描 2 个 TIFF 与 1 个 MP4，显示真实 EXIF/视频时间；跨类型选择后预览并复制成功。
- 文件落盘：`photo/2026/10/08/DSC0001.TIF`、`DSC0001_001.TIF` 与 `video/2026/10/08/CLIP0001.MP4`，大小与 SHA-256 与测试源一致。
- 浏览器：真实 DOM 检查时间图筛选 32 个素材、跨页全选、导出预览 2 个目录、Escape 关闭、类型 Select 的 End/Enter 选择、搜索无结果与清空按钮。
- Select 弹出层：触发器与弹出层均为 120px，水平对齐偏差 0px。
- 390px 窄屏：修复表格最小宽度传播，页面无横向溢出；表格拥有内部横向与纵向滚动。
- 前端严格静态审计：0 错误、0 警告。

Windows/Linux 打包和实际相机硬件尚未实机验证。扩展名识别不等于全部格式的元数据均可解析；未解析日期将回退并标注。

## 缩略图与复制反馈改进

- 原生 macOS JPEG 与 H.264 MP4 取帧测试通过，缩略图尺寸最大 240px，网页中的两类真实缩略图均加载成功。
- 大文件真实复制：768MB 测试文件在界面依次显示 8.3%、42.8% 的复制进度，85.2% 的校验进度，最终成功 100%。
- 取消：复制约 10% 时取消，界面明确显示已取消、未完成 1 个、已保留 0B，文件系统检查未留下部分文件。
- 部分失败：扫描后修改一张测试照片，界面显示 1 个成功、1 个失败，并保留“源文件扫描后已变化，请重新扫描”的具体原因。
- 小文件瞬间完成后保留成功结果弹窗；结果可通过固定任务入口重新查看。
- Progress 组件暴露真实 aria-valuenow；网络状态读取失败不误报复制失败，支持手动重新获取进度。
- 390px 窄屏结果弹窗宽 358px，无横向溢出；重复输入源目录作为导出目标会显示启动失败原因，未复制任何文件。
- 界面刷新后从 Go 服务恢复真实导出结果：成功 2 个、失败 0 个、已处理 100%，结果弹窗仍可查看。
- 新增测试覆盖真实图片/视频取帧、损坏素材、失效 ID、符号链接替换、复制与校验字节进度、校验阶段取消和终态工作量。
- 最终 `go test -race ./...`、`go vet ./...`、TypeScript 检查、Prettier 检查、生产构建与 macOS 应用签名验证通过。新包已写入 `bin/cardatlas.app`，旧包保留在 `work/previous-build`，不强制结束当前客户端进程。

- DESIGN.md 官方 lint：0 错误（镜像 runtime token 的文档组件未逐项引用，产生 orphaned-token 提醒）。

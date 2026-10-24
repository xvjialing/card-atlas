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

## 设备素材导入整合 · 2026-10-23

- 新增通用 Provider、导入规划、持久化任务队列，迁入 Action 6 配对、热点、媒体协议、预览与下载；未修改 osmo-bridge 源工程。
- 最终 `go test -race ./...`、`go vet ./...` 通过。覆盖本地旧功能、通用设备的重名/未知日期/重启恢复/旧设备拒绝/记录损坏/批量恢复、DJI UDP 模拟、HTTP 续传/重定向/长度检查、预览会话隔离及不支持硬链接时的保存检查。
- TypeScript 类型检查、Prettier 检查、前端生产构建及 `git diff --check` 通过。Wails 生产构建使用 `-ts -i` 重新生成类型绑定，应用边界统一将 nil 切片标准化。
- `npm run package` 完成，产物 `bin/cardatlas.app`，`codesign --verify --deep --strict` 通过。没有进行 Developer ID 签名或公证。
- 浏览器 18 项检查通过：配对表单、无效密码阻断、密码显隐、30 项分页、跨页/跨筛选选择、Select 的 Escape 焦点恢复、搜索清空、非法日期范围、导入目标与数量确认、10 项任务分页、单项与批量恢复、刷新失败保留列表、断开确认、兄弟页面状态保留、390px 无横向溢出。控制台无错误。测试通过模块请求拦截注入独立 fixture，验收后已清除模拟接口。
- 严格 UI 静态审计为 0 错误、0 警告；DESIGN.md 官方 lint 为 0 错误、9 条既有 orphaned-token 提醒。运行时色彩仍由 styles.css 拥有，未更改全局 token。

| 设计规则 | 代码证据 | 结论 |
| --- | --- | --- |
| 全局森林绿与语义色 | 设备 CSS 只引用现有变量，沿用 Button/Badge/Progress | 一致 |
| Select/Dialog 统一组件 | 设备页使用既有 Radix 组件和中文标签 | 一致 |
| 页面自然滚动，表格内部滚动 | 设备表格 430px，规划面板在 1150px 以下进入文档流 | 明确的设备页布局变体 |
| 状态和恢复行为 | 设备任务持久化，本地导出记录保留原进程内规则 | 业务差异已记入 UX-CONTRACT.md |

仍未验证：Action 6 实机、相机固件差异、真实系统权限与热点切换、大于 4 GiB 原片、真实 exFAT 外接盘及设备断网恢复。模拟测试、编译与签名结果不能替代这些实机验收。Windows/Linux 无线适配尚未提供。

## 设备素材三视图与任务弹窗 · 2026-10-23

- 设备素材默认网格，可切换列表和时间线；三种视图共用 30 项分页、筛选和选择。时间线按设备日历值分组，“全选当天”覆盖当前筛选的所有分页；未知日期单独分组，支持半选与范围取消。
- 类型与存储改为可见按钮组。Action 6 适配器声明机身存储/存储卡选项，空存储仍可选择；前端不硬编码设备存储编号。
- 移除页面底部的导入记录。提交后自动打开独立任务弹窗，支持本次导入/全部记录、字节进度、暂停与恢复、分页和内联详情；关闭后后台继续，通过顶部入口重开。
- 后端规划返回准确 TaskIDs；本次进度只统计这些任务，不混入历史完成项。新页面会话通过全部记录读取既有任务，不假定能恢复之前的界面批次范围。
- 10 项前端单元测试通过，覆盖日期有效性、排序、跨页日期选择、筛选范围、半选、任务范围和字节进度。Go 全量竞态测试通过；新增规划范围断言后单独重跑 deviceimport 通过，go vet 通过。
- 浏览器验收脚本 36 项通过；另验证未知日期、空存储、减少动态效果与键盘焦点、全部失败共 4 项。包含默认网格、视图切换、跨页选 32 项当天素材、过滤后仅选 11 项机身视频、素材预览焦点恢复、任务自动弹出/后台继续/重开、本次与历史分离、内联详情以及 390px 页面/弹窗边界。模拟接口不访问实机或真实原片。
- TypeScript、Prettier、前端生产构建、严格 UI 静态审计与 git diff --check 通过。DESIGN.md lint 0 错误，保留既有 9 条 orphaned-token 提醒。
- 视觉差异已按用户要求更新 DESIGN.md 和 UX-CONTRACT.md：设备浏览区共用 520px 内部滚动，任务改为弹窗；本地素材分析的 430px 表格与原有导出弹窗保持原行为。全局颜色和字号 token 未改变。
- 新版 `npm run package` 已完成，`bin/cardatlas.app` 已更新并通过 `codesign --verify --deep --strict`。验收结束后清除了浏览器模拟接口，正常浏览器预览和空任务弹窗仍可用。本次不将模拟界面验证表述为 Action 6 实机验证。

## pnpm 工作区迁移 · 2026-10-24

- 通过官方 npm registry 核对 latest 为 pnpm 12.15.0，根 package.json 固定该版本及 Node >=22.13；由现有 Corepack 按项目选择，不改其他工程的版本声明。
- 新增根 pnpm-workspace.yaml 和 pnpm-lock.yaml，将 frontend 作为工作区成员；保留原七天发布缓冲。移除 frontend/package-lock.json 与已迁入工作区配置的旧 .npmrc。
- 从原 npm 锁文件导入，核对 168 个唯一依赖版本全部保留。没有顺带升级 React、Vite 或其他业务依赖。pnpm 的管理器自身依赖单独锁定。
- 安装日志确认使用 `/Users/xvjialing/Library/pnpm/store/v11`，采用写时复制，123 个安装包中复用 117 个、下载 6 个。没有在工程内建立独立 .pnpm-store。
- 旧 npm 安装树原大小为 159888 KiB（约 156 MiB），验证后已删除临时保留的旧树；新安装树和全局仓库通过文件系统共享内容，目录 du 数字不用于宣称净物理磁盘节省量。未清理其他项目、全局 npm 缓存或现有 pnpm 共享包。
- Wails 开发/构建/打包统一调用 pnpm，安装使用 --frozen-lockfile。Docker 交叉编译的前端回退命令同步改为 pnpm，并准备 Node 24 和相同的 pnpm 版本；未构建 Docker 镜像。
- `pnpm typecheck`、`pnpm test`（10 项）、`pnpm build:frontend`、`pnpm package`、应用签名验证通过。生成前端资源文件名/内容哈希与迁移前一致。
- `pnpm dev:frontend --port 9246 --strictPort` 启动成功，浏览器打开设备页和任务弹窗成功，无控制台错误；验证后已关闭预览服务，9245/9246 均无残留监听。
- `pnpm install --offline --frozen-lockfile` 在旧 npm 树删除后通过。版本检查脚本接受 pnpm 12.15.0，拒绝 npm 和旧 pnpm 版本。`git diff --check` 通过。

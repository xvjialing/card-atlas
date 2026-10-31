# 当前版本验证

## 设备素材预览弹窗离线优化 · 2026-10-31

- 按用户截图优化预览显示；用户已明确相机离线，本轮未扫描、加入热点或连接相机，也未启动真机探测入口。
- 抽出 `device-preview-dialog.tsx` 作为设备预览统一所有者，复用既有 Radix Dialog、Button 和 Badge。固定媒体画布 + 桌面信息侧栏，窄屏上下布局；加载、成功、失败和重试的媒体占位保持不变。RAW 缩略图默认适应画布放大，显示实际尺寸与质量说明，支持真实像素尺寸及键盘滚动；视频预定宽高，加载时仍提供用户手势播放入口。
- 新增读取与缓冲反馈、20 秒超时恢复、重新加载及断线返回指引；关闭、断线和会话变化释放旧视频资源。预览绑定选择时的会话上下文，避免新会话读取旧选择；保留现有会话变化关闭预览规则。修复 React StrictMode 重复挂载时资源被提前释放的问题。
- 离线浏览器新增 27 项通过，脚本为 `frontend/tests/device-preview-browser-check.mjs`；覆盖 160×120 缩略图、竖图与 1:1 滚动、视频前后几何、失败重试、缓冲暂停、超时与过期响应、会话和断线、焦点/Escape、390px 长内容、900×650 矮窗口及 reduced-motion。使用纯本地 SVG 与画布生成的 WebM，无页面脚本错误，模拟接口的扫描、连接、导入调用均为 0。既有设备浏览器验收流程也通过。
- `pnpm typecheck`、前端 10 项单元测试、生产构建、Prettier 与差异检查通过。严格 UI 审计 0 错误、0 警告；DESIGN.md 官方缓存 CLI lint 0 错误，保留既有 9 条 orphaned-token 提醒。
- `go test -race ./...` 除既有 `TestNativeVideoThumbnail` 外通过；该测试仍因本机 ffmpeg 缺少 `libssl.4.dylib` 无法生成测试视频。显式排除该测试后全量回归通过，未修改系统依赖或后端测试。
- 模拟截图与报告位于 `work/device-preview-*-desktop.png`、`work/device-preview-mobile.png`、`work/device-preview-browser-results.json` 和 `work/device-preview-ui-audit.json`。本轮为离线界面与恢复行为验证，不替代真实相机媒体加载复测。
- `pnpm package` 完成并更新 `bin/cardatlas.app`，本地签名严格验证通过；已清除浏览器测试拦截并停止本轮启动的预览服务，未启动或重启原生客户端。

## Sony ZV-E10 固件 2.01 PTP/IP 真机调试 · 2026-10-31

- 真机确认：UPnP 描述 HTTP 200，名称为 SonyDigitalMediaServer / SonyImagingDevice；SOAP 控制和目录请求均 404。DigitalImaging 能力明确启用 PTP/IP v3 素材服务。新增按能力选择 PTP/IP 的生产路径，而非继续放宽 SOAP 错误。
- 真机成功项：命令/事件通道、真实型号 ZV-E10 与固件 2.01、1 个存储及 2244 个对象句柄、160×120 JPEG 缩略图、Sony 0x9211 读取 65536 字节。标准 0x101B 在此机返回 0x2009。未导入或保存真实照片，未执行拍摄、删除或曝光等设置变更。
- 完整原生客户端验收通过：2231 项素材（2163 张照片、68 个视频），跳过数 0、清单警告为空；真实 RAW 缩略图已显示，92.01 MB 的 7 秒 MP4 出现画面并播放至 5 秒，关闭视频后再次打开另一张 RAW 缩略预览成功，会话保持连接。已清除测试搜索、关闭预览并留在完整相册，未提交导入任务。
- 修正真机发现的问题：旧 UPnP 描述不再成为 PTP/IP 必经前置；已选一代热点优先直连并用实际型号再校验。Sony 握手后读取 0x9209 状态再开启素材模式；仅在存储未开放时协商模式并等待异步发布。批量元数据在此固件超时，生产改为逐项读取与四分钟总等待；本次型号确认至清单约 130 秒。
- 新增固定依赖 alpha2go v0.3.0（Apache-2.0）；相关 Go 扩展依赖随之更新。许可证、NOTICE 与第三方说明已纳入 macOS 应用资源目录。
- 全部 `internal` 包的竞态回归与 `go vet ./...` 通过；PTP 模拟器验证列表、Sony 分段实际字节、预览取消后的会话可用性、模式释放、型号/地址限制和 64 位范围；能力文档同机边界与既有导入流程通过。最新原生模式优先、状态同步与清理逻辑的相关竞态回归和真机入口静态检查也已通过。
- 已确认调试进程使用当前工程 `bin/cardatlas.app`，本轮界面与素材均来自真机。打包、本地签名严格检查和差异检查通过；重复打包的只读许可证覆盖问题已修复，第三方声明随应用分发。
- 未验收：本素材集合未找到 JPEG，未单独验证 JPEG 原图预览；其他固件、4 GiB 以上文件、真实导入与跨断线续传。上述范围继续保持待实测，不由模拟器结果替代。

## Sony ZV-E10 素材发现与一代菜单修正 · 2026-10-31

- 重试日志确认地址预检通过（`en1`、`192.168.0.5/16`），失败推进到素材发现。旧日志没有响应与描述阶段信息，不能直接断定相机模式或本地网络权限为唯一原因。
- 用户入口为「网络 → 智能手机连接功能 → 连接」。已按一代官方指南明确素材入口为「回放 → MENU → 网络 → 发送到智能手机功能 → 发送到智能手机 → 这个影像/多个影像」，对应 Imaging Edge Mobile；客户端错误提示同步给出准确菜单。
- Sony UDP 与 HTTP 通过 `IP_BOUND_IF` 固定到 Wi-Fi，UDP 通过 `IP_MULTICAST_IF` 显式选择组播出口。保留有线网络；无可用 SSDP 服务时仅尝试当前链路内已知相机的 `:64321/DmsDescPush.xml`。保留机型、同机 URL 与重定向校验。
- 新增 SSDP 开始/接收/过滤计数、描述读取及来源、机型拒绝、识别成功的标准日志，不记录原始载荷或素材。新提示区分机型拒绝、已发现但描述失败和未收到素材服务响应。
- 相关六个包的 `go test -race` 与 `go vet` 通过。新增真实 macOS 内核套接字的 UDP 组播/接口与 TCP 接口校验，以及组播静默/套接字失败后的描述回退、地址和机型边界、取消与诊断日志测试；既有模拟浏览、预览和续传通过。
- `pnpm package` 完成并更新 `bin/cardatlas.app`；本地签名严格验证与 `git diff --check` 通过。未启动新应用或关闭现有实例。
- 本次未切换 Wi-Fi、未远程操作相机；素材相册与预览仍待在正确菜单下实机复测。

## Sony ZV-E10 实机 DHCP 网段误判修复 · 2026-10-31

- 根据实机截图与本机日志定位：热点认证成功，实际地址为 `192.168.0.5/16`、网关为 `192.168.122.1`，此前 `/24` 白名单在媒体发现前错误拦截；并非截图中的 IPv4 配置方式错误。
- Sony 地址预检和素材发现共用按实际 Wi-Fi 掩码选择的地址及网段；拒绝旧网段、自分配地址、设备自身地址、网络/广播地址和已知热点不匹配。保留 DJI 的固定网段检查、素材 URL 同机校验和重定向限制。
- 提示区分“缺少可用地址”与“已有地址但地址或掩码不匹配”，后者附实际地址；已使用 DHCP 时提示续租。进入发现阶段后记录接口、源 IPv4 和实际网段。
- `go test -race ./internal/platform ./internal/devices/zve10 ./internal/upnpav ./internal/devices/wifihost ./internal/deviceimport ./internal/dji/transfer` 通过；同范围 `go vet` 通过。新增截图 `/16`、原有 `/24`、错误掩码、广播边界、热点检查及 DHCP 地址切换用例；既有模拟相册、预览和续传回归通过。
- `pnpm package` 完成，生成 `bin/cardatlas.app`；`codesign --verify --deep --strict` 与 `git diff --check` 通过。未启动新应用或关闭用户当前实例。
- 本次没有切换系统 Wi-Fi 或连接相机；真实媒体服务发现、相册清单和预览仍待使用修复后的客户端复测。

## Sony ZV-E10 协议完善与离线验证 · 2026-10-28

- 对照 Sony ZV-E10 官方发送素材说明、Monitor+ 官方能力/连接文档和 ImagingEdge4Linux 的协议行为，保留既有 Provider 与队列架构，修正此前“配网成功即可取得原片”的假设。Monitor+ 能力表明确一代 ZV-E10 无 Camera Content Access，不能将其无线监看流程等同于素材传输。参考与逐项分析见 `docs/device-import.md` 的 Sony 章节。
- 修复：双热点网段、Wi-Fi 源地址绑定、独立 SSDP 数据报、发现/描述/控制/资源同机约束与禁止重定向、PushRoot/PhotoRoot/0 入口协商、短页截断、根目录混合条目漏读、深层目录及循环、资源质量/元数据错配、RAW 标题对应 JPEG 的扩展名、临时资源 URL 引起的 ID 变化、无扩展名视频预览、空选择、GET 活性探测、会话取消及重复关闭。仅在相机明确发布 XPushList 时协商传输通知。
- 二维码保留已有 Sony W01 格式，新增图片字节/像素上限，修正密码裁剪和重复反转义；测试凭据改为内存替身，不访问系统钥匙串。未引入其他项目的代码依赖。
- Sony 欢迎页与连接弹窗改为“发送到智能手机”语境，随设备展示连接方式、手机应用和存储能力，移除已过期的 Action 6 专属介绍和“索尼后续扩展”文字；提示发送质量由相机设置决定。ARW 预览读取设备缩略图，原文件导入后本机查看。
- 回环 HTTP/UDP 模拟验证：二维码、发现数据报、描述端点、SOAP 入口/分页/目录变化、缩略图、视频 Range、JPEG/视频实际文件落盘、断流续传、忽略 Range 时完整重下、内容与 SHA-256、临时文件清理、转换版本提示、素材快照、失效会话、失败刷新保留旧清单及传输生命周期。没有连接真实设备或切换系统 Wi-Fi。
- Sony/UPnP/平台及共享导入流程的竞态测试通过；`go vet ./...` 通过。完整 `go test -race ./...` 中仅既有 `TestNativeVideoThumbnail` 失败：本机 Homebrew ffmpeg 9.0.2_1 缺少 `libssl.4.dylib`，在生成测试视频时退出。显式运行 `go test -race ./... -skip '^TestNativeVideoThumbnail$'` 后其余全量测试通过；未改动该测试或系统依赖。
- TypeScript 检查、前端 10 项单元测试、生产构建、格式检查及 `git diff --check` 通过。严格 UI 审计 0 错误、0 警告，报告位于 `/private/tmp/cardatlas-sony-premium-audit.json`。现有 DESIGN.md 已核对，本次未改变视觉 token 或控件所有者。
- 浏览器模拟检查 53 项通过、页面错误 0：包括设备切换、Sony 欢迎与模式提示、质量说明、无蓝牙入口、二维码密码、损坏照片、减少动效、390px 窄屏及 Escape 焦点归还，继续覆盖列表/筛选/任务/刷新失败等共享流程。截图保存在 `work/sony-connection-offline-mobile.png` 与 `work/sony-connection-offline-desktop.png`。
- `pnpm package` 完成，生成 `bin/cardatlas.app`；本地 ad-hoc 签名严格校验通过，未启动新应用或触发设备连接。
- 未实测：ZV-E10 固件描述/目录入口、XPushList 实际行为、选片范围与 RAW/代理/原视频组合、4 GiB 以上原片、设备真实 Range、热点认证、macOS 权限和网络恢复。兼容状态保持“已接入，待实机验证”。


## Mini 5 Pro 实机反馈与蓝牙授权接入 · 2026-10-26

- 实机反馈：Mac 加入 DJI-MINI5PRO-B7AC 热点后素材会话报“相机未响应”。查证 DJI 官方指引确认：手机快传首次连接需要长按机身电源键约 3 秒授权，授权发生在蓝牙配对层——此前 Mini 5 Pro 适配器缺少蓝牙授权环节。
- 接入实验性蓝牙授权：`camera` 包抽出扫描过滤与配对内核，新增 `ScanAircraft`（DJI 厂商数据或名称匹配）与 `PairAircraft`（授权文案为“长按电源键约 3 秒”，与手机快传一致）；`wifihost` 新增 Prepare/Discover/SetIdentity/SetHint/Teardown 钩子，配对成功后固化蓝牙标识用于任务归属与续传，热点提示参与扫描排序；Mini 5 Pro 连接面板提供蓝牙授权入口并保留热点扫描与手动接续。
- 素材会话失败文案改为无人机语境：提示先完成电源键授权、允许本地网络，或改用 microSD 卡经素材分析导入。兼容性标记更新为“实验接入：授权与协议待实机验证”。
- 局限：无人机蓝牙 GATT 服务与 FFF0 协议是否与 Osmo 一致未经实机确认，配对或素材会话仍可能失败；失败时会给出明确提示与 SD 卡兜底路径。`go test -race ./internal/...` 全绿（新增 wifihost Prepare 流程用例），浏览器验收 44 项通过，应用已重新打包。

## 切换 Wi-Fi 前的确认弹窗 · 2026-10-26

- 提交热点密码后、切换 Wi-Fi 前弹出确认：标题“即将切换 Mac 的 Wi-Fi”，说明互联网可能暂时中断、断开设备后尝试恢复原网络；提供“不再提醒”（记住选择并继续）与“继续连接”（本次继续）两个动作，仅关闭弹窗不提交且保留已填密码。
- “不再提醒”存入 webview localStorage（键 cardatlas.wifiSwitchNotice.dismissed），已在 UX-CONTRACT.md 记录该例外；路径、筛选与选择等业务数据仍不进入 localStorage。
- 浏览器验收 45 项通过（新增弹窗与双按钮检查），TypeScript 检查通过，应用已重新打包。

## ZV-E10 实机二维码格式接入 · 2026-10-26

- 实机照片诊断：ZV-E10（Imaging Edge Mobile 配对界面）二维码解码为索尼私有 `W01:S:RLE1;P:…;C:ZV-E10;M:…;` 格式，此前解析器不识别导致“二维码中未找到 Wi-Fi 连接信息”。已扩展解析器：按 `DIRECT-<S>:<C>` 重组完整热点名，新增热点后缀片段匹配兜底（wifihost `ProvisionFragment`），空密码加入时优先使用二维码密码。
- 用实机照片回归验证：解码出 `DIRECT-RLE1:ZV-E10` 与 8 位密码，与相机屏幕显示的 SSID 一致。解析失败的提示改为引导“在相机上选择「通过密码连接」查看密码后手动输入”。
- `go test -race ./internal/...` 全绿；zve10 新增索尼 W01 格式与片段解析用例，wifihost 新增片段匹配加入用例。实机 Wi-Fi 加入、UPnP 素材浏览与下载仍待后续实机验证。
- 实测反馈“选中热点后仍要求填密码”：二维码密码本就支持留空直连，但界面未表达该状态。后端 `Network` 新增 `hasProvisionedPassword`，连接面板在二维码密码就绪时显示“已从二维码照片读取该热点的密码，无需输入”，按钮变为“使用二维码中的密码连接”，可切换手动输入。浏览器验收 43 项通过（新增二维码就绪态检查），`pnpm package` 重新打包并通过签名验证。

## 新增 DJI Mini 5 Pro 与 Sony ZV-E10 导入 · 2026-10-25

- 按既有 Provider 架构新增两台设备的无线导入：DJI Mini 5 Pro（热点直连，仅存储卡，复用 DJI 传输协议）与 Sony ZV-E10（二维码照片导入 + 热点直连，经 UPnP AV 媒体服务浏览与下载）。两台设备兼容性均标记“已接入，待实机验证”，不宣称实机验证。
- 抽出 `internal/devices/wifihost` 通用热点会话状态机（扫描分类、确认加入、钥匙串凭据、手动接续、断开恢复原网络、链路中断呈现）；Action 6 的蓝牙专用实现保持不变。`transfer` 抽出 `DownloadURL` 供非 DJI 地址复用同一套续传、长度核对与发布机制；`platform.WaitDeviceWiFi` 泛化设备网段（索尼 10.0.0.0/24）。Action 6 的预览代理迁入共享 `internal/dji/preview`，行为由既有测试守护。
- ZV-E10 连接二维码：连接表单可导入相机“连接智能手机”二维码的照片（前端缩放为 JPEG 数据 URL，`ConnectionRequest.Photo` 传递，适配器内 gozxing 解码），支持 Android Wi-Fi 格式与常见键值/中文格式，读取结果仅预填热点信息，仍需用户确认才切换 Wi-Fi；密码与手动输入同样入钥匙串。素材地址必须与相机同址，预览与下载拒绝其他主机。
- 设备页在未连接时展示设备切换列表（radiogroup），连接对话框按设备呈现蓝牙、热点扫描或二维码入口；预览路由按 provider 分发到三个适配器。
- `go test -race ./internal/...` 全部通过（wifihost 10 项、mini5pro 6 项、upnpav 5 项、zve10 7 项新增；既有 deviceimport/action6/dji/transfer/platform 全绿）。`go vet ./internal/... .` 通过。根包 `TestNativeVideoThumbnail`（原生取帧用例）在本次改动前的干净基线上同样失败，属既有环境问题，与本次无关。
- TypeScript 类型检查、前端单元测试（10 项）、生产构建通过。浏览器验收 42 项通过、控制台无错误：既有 36 项之外新增设备切换列表、ZV-E10 二维码入口、Mini 5 Pro 热点扫描入口与切回 Action 6。修复既有“组织方式下拉菜单 Escape 归还焦点”检查的调度竞态（改为 waitForFunction 轮询，纯基线在负载下同样复现）。
- 未验证：Mini 5 Pro 与 ZV-E10 实机（热点认证、清单格式、缩略图、Range 续传、固件差异）；模拟测试与编译结果不替代实机验收。未运行 `go test -race` 与重新打包。

验证时间：2026-10-25，macOS Apple Silicon，Go 1.27，Node 24，Playwright headless chromium。

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


## 2026-10-26 Mini 5 Pro 连接与保活修正

- 实机：Wi-Fi en1 已取得 192.168.2.88，192.168.2.1 路由为 en1，有线 en0 仍联网；TCP 80 可达；UDP 9004 拒绝，9003 正常返回 9 字节握手。目录探测未收到素材会话挑战或清单。
- 用户报告无人机过热关机后已停止全部实机测试，并退出旧 CardAtlas 进程。未采集温度，未断言保活是过热唯一原因。用户确认仍在冷却，暂不继续实机测试。
- 已修正飞行器独立端口与 WLM 寻址、DJI FLY 配对标识、安装身份落盘、Wi-Fi 接口绑定；失败释放蓝牙，热点选择等待 90 秒停止唤醒。参考 Osmosis #40 与未合并 PR #24，未声称 Mini 5 Pro 端到端已验证。
- 新增回环模拟器验证动态设备地址、单调 WLM 计数器、双向应答、跨 UDP 数据重组、重复包过滤、目录槽释放；网络握手不能被当作素材会话成功。其余新增测试覆盖配对标识、身份持久化、失败和超时清理、旧定时器隔离、源地址选择及原文件预览。
- `go test -race ./internal/dji ./internal/devices/mini5pro ./internal/devices/wifihost ./internal/platform ./internal/dji/preview` 通过。
- `go test ./...` 的全部 internal 包通过；主包 `TestNativeVideoThumbnail` 未通过，原因是系统 ffmpeg 9.0.2_1 依赖的 `/opt/homebrew/opt/openssl@4/lib/libssl.4.dylib` 缺失，应用取帧本身不依赖 ffmpeg。没有修改测试或全局工具链掩盖该失败。
- 前端生产构建通过；Go 生产构建通过；`bin/cardatlas.app` 已签名并通过 `codesign --verify --deep --strict`。编译仍有既有的重复 `-lobjc` 链接提示。
- 待验收：冷却后进行短时连接，验证真实素材目录及下载。若仍只收到身份广播，需对照 Android DJI Fly 的成功快传通信；不能仅根据参考机型猜测文件路径或认为空目录。


## 2026-10-26 离线诊断日志功能

- 新增 DiagnosticsService 和侧栏“诊断日志”页；应用、框架与设备共用同一个日志 Store，记录级别变更立即生效，保持既有 7 天/100 MB 留存。新文件使用 cardatlas 前缀，继续兼容 osmo-bridge 历史文件。
- 后端测试覆盖无网络条件下落盘、新 Store 模拟重启读取、实时共享记录级别、脱敏、20 次/分钟的界面错误限流、独占 ZIP 导出及路径失败；日志专用测试与竞态检查通过。
- 全量 `go test -race ./...`：全部 internal 包通过；主包唯一失败是 TestNativeVideoThumbnail，系统 ffmpeg 缺少 libssl.4.dylib。未修改该测试或全局依赖。测试发现的“Busy=false 早于操作锁释放”竞态已修复并复测。
- `pnpm typecheck`、`pnpm test`（10 项）、`pnpm build:frontend`、改动前端文件 Prettier 检查、git diff --check 通过。
- 浏览器接口替身验证 16 项：50 条分页、历史加载、详情键盘展开、级别筛选、过期响应隔离、中文输入法、清空焦点、读取失败保留内容、级别更新、导出忙碌/失败/取消、打开目录、离线状态、本机时间、窄屏长内容与下拉边界、切换页面保留设置。补测了键盘 Select、空结果及后端写入失败告警。截图及结果位于 frontend/work/diagnostics-*。
- 产品 UI strict 静态审计无错误。官方 designmd 在线工具的下载执行被自动审批阻止（未核验外部包的供应链风险）；未绕过限制，使用已有本地审计并人工核对 DESIGN.md 与 CSS token。
- 生产应用已构建并通过签名校验；安装前的版本备份在 work/logging-before-fix/cardatlas.app。未执行任何无人机连接或过热复测。

- 原生客户端验收：在 /Applications/cardatlas.app 的真实 Wails 窗口打开“诊断日志”，读取到 10 个日志文件（含历史记录与本次启动事件）；通过原生目录选择器导出 work/CardAtlas-诊断-20261026-231106-3202360028.zip，ZIP CRC 校验通过，仅包含 diagnostics.json、events.jsonl 和说明.txt。已确认导出元数据的 Wails 版本从构建信息读取。

## 2026-10-26 UDP EPIPE 恢复

- 系统日志确认首次 EPIPE 与 nehelper 刷新已有本地网络允许策略同步；后一次应用请求完成握手。接口绑定开/关的 Go 与 Python 短探测均成功发送，未操作系统权限。
- 故障注入覆盖：首次 EPIPE 关闭旧 socket 后以新 socket 恢复握手；持续 EPIPE 最多重建一次；等待期间可立即取消；EACCES/EADDRINUSE/ECONNREFUSED 不重试。
- `go test -race ./internal/dji ./internal/devices/mini5pro ./internal/devices/wifihost ./internal/platform`、前端 typecheck、生产构建、strict UI 静态审计通过。
- 保留 14 秒整体会话预算。完整素材会话尚未通过；用户已确认 DJI Fly 可显示素材，下一步需要成功会话的 Android PCAP。旧应用备份在 work/epipe-before-fix/cardatlas.app。


## 2026-10-27 Mini 5 Pro 成功抓包回归

- 输入为用户从 DJI Fly 首次授权到素材阅览的 PCAP，18,626 包；分析范围是无人机的 IP 流量，不含 BLE HCI/GATT。完整分析与原包号见 docs/mini5pro-capture-20261027.md。
- 证据驱动修正：11 字节开启请求、51 字节身份请求、分别确认客户端 ACK 与反向身份请求、51/41 能力协商、广播路由82与会话单播E2、控制ACK窗口、回放状态、变长目录、type1 DNG、双存储、相对HTTP路径与嵌入JPEG Range。没有重放飞控/激活/国家码/时钟初始化。
- 15个匿名会话报文CRC重新计算；目录样本包括SD45条、机身45条、连拍57条及全景1条，替换原文件名和拍摄日期。离线模拟器以这些布局验证完整连接状态机与跨UDP分片读取。
- DJI、预览、传输、Mini 5 Pro、Action 6、平台层竞态回归通过；前端 typecheck、10项前端测试、生产构建和strict UI审计通过。全量go test -race ./...中全部internal包通过，主包唯一失败仍是ffmpeg缺少libssl.4.dylib的原生视频缩略图测试。
- 本次未连接无人机；完整实机连接与原片下载仍待冷却后的短时验证。安装前版本备份目录为work/capture-before-fix/cardatlas.app。


## 2026-10-27 第二份普通重连及下载抓包

- 文件 SHA-256：3cb8f587d0e82f5dad8f51b0edebf7ec7838f3ae8a2e73f0880b87f2a723e8eb。88,247包，无捕获截断。
- 流式TCP核对22个HTTP响应完整；DNG 57,315,328字节、MP4 375,512,501字节均由5段连续完整覆盖，未保存媒体正文。
- 50条51/17均属于旧Mac端点，手机未回复仍成功；撤销试验地址替换和ACK，并加入带真实设备广播上下文的旧通知隔离回归。
- 候选修正采用本机配对标识前缀和稳定WLM地址，补齐捕获中的只读启动查询，优先响应设备的能力请求，保留有界主动发现回退。
- go test -race ./internal/dji ./internal/devices/mini5pro ./internal/dji/preview ./internal/dji/transfer通过；旧通知隔离测试通过。新候选没有替换安装版，尚待新一轮实机验证。
- 细节见 docs/mini5pro-reconnect-20261027.md。

## 2026-10-27 17:23 Mini 5 Pro 冷却后短测

- 用户确认冷却、手机断开并完成热点准备后，运行8秒候选探测；Wi-Fi地址192.168.2.88，UDP握手成功，无EPIPE。记录782个输入和165个输出数据报，结束后9003端口无占用。
- 基础能力请求39次、应答37次；完整能力请求4次均指向其他端点。未收到挑战、双向身份确认或目录分片。候选稳定地址方案仍未通过，不安装，不标为修复完成。
- 普通日志补充其他目的地址的计数与有界概要、蓝牙配对状态码；不会自动接纳其他端点或写入原始身份/凭据。新回归覆盖重复错误路由不应答、不改绑、概要只写一次，以及后续合法请求正常应答。
- 实机录制工具在会话或目录失败时设置测试失败，避免录制完成的PASS掩盖设备连接失败。
- `go test -race ./internal/dji ./internal/devices/mini5pro ./internal/devices/action6 ./internal/diagnostics`通过；回环网络模拟需在允许本地端口监听的环境执行，未启用实机测试标签。`git diff --check`通过。
- 详细包号与结论见 docs/mini5pro-reconnect-20261027.md；原始记录仅留本机work目录。

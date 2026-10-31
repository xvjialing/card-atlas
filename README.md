# CardAtlas · 影像归档

使用 Go + Wails V3 的中文桌面客户端。分析相机存储卡、已挂载机内存储或本地素材文件夹，按日期与文件选择复制整理。

工程目录：`/Users/xvjialing/projects/card-atlas`。

## 已实现

- 通用“设备导入”入口：macOS DJI Osmo Action 6、Mini 5 Pro 和 Sony ZV-E10，兼容状态以设备描述和验证记录为准。
- Sony ZV-E10 支持二维码配网、实际 Wi-Fi 掩码与接口绑定，兼容 UPnP 和 PTP/IP 素材路径。固件 2.01 已真机验收 2231 项相册、RAW 缩略预览和视频播放；真实导入及大文件续传仍待验收。记录见 [Sony 真机调试](docs/device-import.md#sony-zv-e10-固件-201-ptpip-真机调试2026-10-31)。
- 蓝牙配对、真实热点选择、钥匙串密码和手动 Wi-Fi 接续，内置存储与 SD 卡素材浏览、筛选和预览。
- 设备素材支持列表、网格、时间线，按天跨页选择，直接切换机身/存储卡及图片/视频筛选；导入进度与记录在独立任务弹窗中展示。
- 日期归档或保留设备目录；持久化导入记录、暂停续传、批量恢复、重试、重名保护和本地 SHA-256 指纹。
- 设备协议与通用导入队列分层。产品流程、架构及限制见 [设备素材导入](docs/device-import.md)。

- 原生文件夹选择，也可粘贴路径；递归扫描，后台任务可取消。
- 照片、视频数量、容量与拍摄日期分布；时间图可点击筛选。
- 日期闭区间、类型、文件名/路径搜索、日期/大小/名称排序、30 条分页。
- 单文件、当前页、全部筛选结果选择；跨页保留选择。
- 素材真实缩略图（macOS 原生照片解码与视频取帧，包含方向处理）、文件详情与导出预览。
- 独立复制弹窗：复制/校验字节进度、当前文件、成功/失败数量、具体错误、取消和本次运行导出记录。
- 照片按 `photo/YYYY/MM/DD/原文件名`，视频按 `video/YYYY/MM/DD/原文件名` 复制。
- 保留源文件；目标重名追加 `_001`、`_002`；复制后读回并验证 SHA-256。
- 源文件变化检测、目录范围保护、错误逐文件处理，不会覆盖已有文件。
- 明确标记的浏览器演示模式，仅预览界面，不操作本地文件。
- 独立品牌 Logo；调试、打包和直接启动二进制均内嵌 Dock 图标，并同步侧栏与网页图标。

## 技术版本

Wails Go 库与前端 runtime 固定为 `v3.0.0-beta.29`（2026-10-14 经 Go module `@latest` 查询，V3 仍为 Beta）。React / React DOM `19.3.0`，Tailwind CSS `4.3.3`，Vite `8.3.4`。shadcn/ui 通过 CLI 生成，使用 Radix `1.8.1`；具体版本以根目录的 `pnpm-lock.yaml` 为准。

## 直接运行 macOS 应用

```sh
open /Users/xvjialing/projects/card-atlas/bin/cardatlas.app
```

已在当前 macOS Apple Silicon 环境构建并以本地签名打包，完成原生客户端扫描、同名文件复制与内容校验测试。此构建最低 macOS 13，未做 Developer ID 签名和公证。Windows/Linux 尚未进行实机验证。

## 开发与构建

应用图标会在调试与打包时自动从同一原图生成，不需要手动拷贝。原图、1024px 主图和生成提示词保留在 `assets/brand/`，导出脚本为 `scripts/brand/main.go`。调试版和正式版使用独立的 macOS bundle identifier，图标设计保持一致。图标显示在底部 Dock；替换应用包后需退出当前实例再启动。

环境：Go 1.27+，Node 22.13+（建议 24），pnpm 12.15.0，平台原生工具链。macOS 需要 Xcode Command Line Tools；其他系统依赖见 [Wails 安装说明](https://v3.wails.io/quick-start/installation/)。

```sh
cd /Users/xvjialing/projects/card-atlas

# 启用项目声明的 pnpm 版本并按锁文件安装工作区
corepack enable pnpm
pnpm install --frozen-lockfile

# 开发运行（原生客户端，支持热更新）
pnpm dev

# 打包本机桌面应用
pnpm package
```

以上命令均在项目根目录执行，无需手动进入 `frontend`。根 `package.json` 的 `packageManager` 固定 pnpm 版本；Wails 开发与打包任务使用 `pnpm install --frozen-lockfile` 检查整个工作区依赖，并将正确版本的 Wails CLI 安装到工程 `.tools/`。首次运行需联网下载依赖；开发启动后通过 Wails 原生窗口使用真实文件功能。macOS 打包产物为 `bin/cardatlas.app`。

其他常用命令：

```sh
# 构建本机可执行程序
pnpm build

# 后端测试（包含竞态检测）
go test -race ./...

# 前端类型、单元测试与生产资源构建
pnpm typecheck
pnpm test
pnpm build:frontend
```

浏览器界面预览：`pnpm dev:frontend`，访问 `http://127.0.0.1:9245/?demo=1`。本地文件扫描与导出需要桌面客户端。

## 依赖管理与磁盘复用

- 根目录 `pnpm-workspace.yaml` 管理根包和 `frontend`，统一使用根 `pnpm-lock.yaml`；不再提交 npm/yarn 锁文件。
- 从既有 npm 锁文件导入依赖版本，不借迁移升级 React、Vite 或其他业务依赖。保留七天发布缓冲，依赖构建脚本按工作区配置显式处理。
- 使用 pnpm 默认用户级内容仓库；macOS 通常位于 `~/Library/pnpm/store`。同卷安装使用写时复制或硬链接复用包内容，项目内的链接目录仍可能被 `du` 计入完整文件大小，因此目录大小不等于新增物理磁盘占用。
- 不在工程内另设 store，也不使用 `node-linker=hoisted` 退回 npm 式平铺复制。受限代理环境若无法写共享仓库，需要授权访问仓库后再安装，避免 pnpm 退回 `node_modules/.pnpm-store`。
- 日常新增依赖：`pnpm --dir frontend add 包名`；开发依赖加 `-D`。恢复依赖：在根目录运行 `pnpm install --frozen-lockfile`。
- 根目录与前端的安装检查会拒绝 npm/yarn 或不匹配的 pnpm 版本。更新 pnpm 时同步根版本声明与 Docker 交叉编译镜像中的安装版本。

## 日期与格式

照片支持 JPEG、PNG、HEIC/HEIF、TIFF、WebP、AVIF、BMP、GIF、DNG、CR2/CR3、NEF、ARW、RAF、ORF、RW2、PEF、SRW。视频支持 MP4、MOV、M4V、MTS/M2TS、AVI、MKV、MPG/MPEG、WMV、WebM、3GP、MXF。

格式识别依据扩展名，不进行转码或媒体解码。内置 EXIF 用于 JPEG/TIFF 和部分 TIFF 系 RAW；HEIC、CR3 等未能解析拍摄时间的文件使用修改时间。MP4/MOV/M4V/3GP 优先解析 `moov/mvhd` 创建时间，UTC 转为本机时间。相机 EXIF 无时区时按本机本地时间解释。所有回退都标明“文件修改时间”，用户可据此确认归档日期。

## 行为与边界

- 已适配的无线设备使用“设备导入”。其他设备可使用已挂载目录；仅提供 MTP/PTP 的设备暂需先通过系统工具导入到本地。
- 不跟随扫描中的符号链接；隐藏文件夹、隐藏文件、非媒体文件会跳过。无权限文件显示警告。
- 输入与输出目录不能相同、互相包含，避免再次扫描已导出的文件。
- 取消导出保留已完成文件并清理当前不完整文件。强制退出、断电或设备拔出无法保证清理当前文件，需检查结果。
- 重复导出自动编号，不执行自动去重。SHA-256 验证复制写出的字节；不是文件来源鉴定。
- 导出记录在进程内保留最近 20 次，最多展示每次 100 条错误，退出后清空。
- 筛选时保留原有选择，导出按全部已选文件执行。成功重新扫描后清空旧选择。
- 浏览器演示数据不代表真实用户素材，演示导出仅显示提示。

## 工程结构

```text
main.go                 Wails 应用与窗口
mediaservice.go          扫描、分页、选择、任务与复制服务
metadata.go              EXIF 与 MP4/MOV 时间解析
mediaservice_test.go     文件、元数据、冲突与取消测试
frontend/src/App.tsx     分析工作台与导出流程
frontend/src/components/ui/  shadcn/ui 共享组件
frontend/bindings/      Wails 自动生成的 TypeScript 绑定
build/                  各平台打包配置
scripts/wails.sh         固定版本 CLI 包装
package.json            项目根目录开发、构建与打包命令
DESIGN.md               视觉规范与 token 对应关系
UX-CONTRACT.md          状态、选择、日期与文件操作契约
VALIDATION.md           当前版本验证记录
```


## 离线诊断日志

侧栏打开“诊断日志”。客户端会自动将应用启动、设备连接、素材扫描/导出和运行异常记录到本机；断网时无需额外操作，重启后仍可查看。默认标准记录，保留 7 天、最多 100 MB，并自动滚动。旧版设备日志仍能读取。

排查连接问题时先切换为“详细记录”，复现后点击“刷新”，可按级别或关键词筛选并展开详情。“导出诊断包”会让你选择本地文件夹，保存一个独立 ZIP，包含保留期内全部日志、版本信息和说明；不受页面筛选影响，不覆盖已有文件。之后可手动提供此包进行排查。日志会对密码、配对标识、已知热点名和路径脱敏，不打包照片视频，也不会自动上传。

日志默认保存在 macOS 用户配置目录 `CardAtlas/devices/logs`，可直接点击“打开日志文件夹”。如果页面提示写入异常，请检查磁盘空间和目录权限。

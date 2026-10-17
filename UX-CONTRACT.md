# CardAtlas 交互契约

业务来源：用户要求通过分析路径筛选照片和视频，复制到 `类型/年/月/日`。视觉规则见 DESIGN.md。

| Capability | Canonical owner | Source of truth | Allowed variants | Verification |
|---|---|---|---|---|
| Table Selection | App.tsx selected Set + MediaService.MatchingIDs | 用户文件选择要求 | 当前页 / 全部筛选 | 浏览器跨页选择 |
| Select/Listbox | components/ui/select.tsx（Radix） | DESIGN.md | authored | 键盘、Escape 与弹出层 |
| Date | App.tsx 原生 date 输入 | 拍摄日期闭区间 | native | 日期边界测试 |
| Form | shadcn Input + MediaService 路径校验 | 用户路径选择要求 | 本地路径 | 后端路径错误测试 |
| Scrollbar | src/styles.css | DESIGN.md | 全局 / 表格几何 | 计算样式和窄屏 |
| Toast | App.tsx message 区域 | DESIGN.md | status / alert | 空、失败与成功反馈 |
| Export | MediaService + Dialog | 用户复制归档要求 | 校验后完成 / 取消 | 内容、冲突与取消测试 |

文件仅在本地访问；普通挂载目录为输入，MTP/PTP 未挂载设备需先导入。扫描忽略符号链接、隐藏项目与非媒体；读取失败保持部分结果和警告。JPEG/TIFF/部分 RAW 优先 EXIF，MP4/MOV/m4v/3gp 优先 mvhd；其他日期回退到文件修改时间并标注来源。本机时区负责日期归档。重复导出生成新序号，不自动去重。

扫描/导出使用后台 goroutine，前端每 400ms 顺序查询进度。读取失败递增间隔重试至多 5 次，然后提供手动重新获取进度，不将状态读取失败误报为复制失败。查询通过 active guard 忽略过期响应，搜索防抖并兼容 IME。取消扫描保留之前成功扫描；取消导出保留已完成文件，删除当前未完成文件。强制退出无法保证清理当前文件。导出失败逐文件继续并记录前 100 条明细。导出记录只保留本次进程最近 20 条，不写入用户存储。

路径筛选和选中文件具有本地敏感语义，不放在 URL 或 localStorage。用户切换界面保留筛选。分页固定 30，筛选重置到第一页，后端返回真实总数。桌面最小宽度 900px；浏览器预览适配到 390px。所有导出按已选文件精确执行，选中数量跨页计数。

应用为新工程，无现有兄弟页面；列表、导出记录、使用说明复用同一导航、页面标题、反馈与控件系统。

缩略图由 MediaService.GetThumbnail 只接受已扫描文件 ID；使用已验证文件句柄，macOS 用 ImageIO / AVFoundation 原生解码。限制两个并发任务和 128 条 LRU 缓存。列表只加载可见行，视频按需随机读取，解码读取预算 64MB。其他系统仅有 Go 图片解码，视频原生取帧未实现；格式或文件不可读时明确回退图标。

导出开始后立即打开 ExportTaskDialog；总进度以复制与读回校验字节为两个阶段计算，校验成功前不显示成功。终态按成功、部分完成、全部失败或取消显示，保留具体错误。结果读取必须在提交终态之前完成，避免 effect 清理丢失完成反馈。关闭弹窗不取消任务，固定入口可随时查看；刷新界面恢复当前任务与导出记录。

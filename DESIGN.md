---
version: alpha
name: CardAtlas
description: 相机素材整理工作台，以存储卡、资料柜和纸质索引为视觉参照。
colors:
  primary: "#28634c"
  primary-foreground: "#ffffff"
  background: "#f7f8fa"
  foreground: "#242e2c"
  card: "#ffffff"
  secondary: "#eff2f1"
  accent: "#e5eee9"
  muted-foreground: "#68746f"
  border: "#e1e6e3"
  destructive: "#b13b32"
  photo: "#60917b"
  video: "#c88745"
typography:
  sans:
    fontFamily: '"Avenir Next", "PingFang SC", "Microsoft YaHei", sans-serif'
  mono:
    fontFamily: '"SFMono-Regular", "Cascadia Code", monospace'
rounded:
  sm: "6px"
  md: "8px"
  lg: "12px"
  xl: "16px"
spacing:
  section-gap: "20px"
  page-max: "1660px"
components:
  button: {}
  input: {}
  dialog: {}
  select: {}
  checkbox: {}
---

# CardAtlas 设计规范

## Overview
面向在电脑上整理存储卡与挂载存储的摄影用户。使用中文简体，zh-CN 数字，本机时间。产品工作台而非营销页；特色是低饱和森林绿、暖棕视频标记、归档目录树与索引数字。避免紫色渐变、大量装饰卡片和假素材缩略图。

## Colors
运行时 `frontend/src/styles.css :root` 是唯一 CSS token 所有者，本文件记录对应值。`@theme inline` 将变量映射到 Tailwind 与 shadcn/ui；所有交互组件使用语义色。primary 为操作与选择色，photo/video 为分布图颜色，图表同时提供数量文字与 accessible label。危险状态附带图标和文本。

## Typography
无远程字体，避免启动字体闪动。中文采用苹方或微软雅黑，西文采用 Avenir Next。路径采用 mono，数字使用 tabular-nums。标题 23–30px，主体 12–14px；不对中文应用斜体。

## Layout
208px 侧栏，主内容最大 1660px，列表与 287px 导出面板并排。960px 以下导出面板进入文档流，700px 以下导航变为顶部三列网格。页面自然滚动；文件表格独立拥有 430px 内部滚动区域，30 条分页。帮助页面不继承表格高度。全局滚动条由样式表控制，悬浮和按下状态明确。

## Elevation & Depth
大部分面板通过背景和 1px 边界划分，静态数据不加阴影。原生弹窗与菜单沿用 shadcn/Radix 的层级、焦点管理。欢迎页存储卡图形允许轻量阴影。

## Shapes
控件 8px，面板 12px；图标使用 Lucide 线性风格。素材列表显示真实照片缩略图或视频帧，固定 60×44px，保留格式标签与视频播放标记。无法解码时回退到格式图标并说明原因，不伪造预览。

品牌标识为森林绿存储卡、米白镜头光圈与暖金索引标签，使用 `assets/brand/cardatlas-original.png` 作为美术源文件。保留透明外缘和米白圆角底板；侧栏使用统一导出的 256px PNG，以 48px 图像盒显示。装饰性的 Lucide 光圈图标仍可用于分析页面，不作为系统应用图标。

## Components
Button/Input/Checkbox/Dialog/Select/Progress/Badge 由 shadcn CLI 生成。按钮提供 hover、focus-visible、active、disabled 和 busy；加载保持按钮宽度。查询反馈统一使用 message 区域与 live region。原生日期控件明确由桌面操作系统拥有日历语言和几何；产品日期格式在列表和输出中保持一致。所有自有文本及可访问名称为中文；Radix 默认关闭标签改为中文。搜索可立即清空，输入法期间不提交搜索，300ms 防抖。导出前使用 Dialog 预览具体目标、文件数量和目录。

动效仅用于任务转圈与扫描进度；尊重 reduced-motion。图表按日展示，长区间按月汇总，提供横向滚动而不删数据。

导出任务复用 shadcn Dialog 和 Progress，通过 `components/export-task-dialog.tsx` 显示复制与校验阶段、字节进度、成功/失败/未完成数量。完成结果保留到用户关闭；关闭后固定任务入口可重新打开。进度不能用单个大文件完成数量代替，Progress 根节点暴露真实 value 给辅助技术。缩略图由 `components/media-thumbnail.tsx` 统一负责按可见区域加载，避免加载改变行高。

品牌资源由 `scripts/brand/main.go` 单向导出到 Go embed、前端 PNG 与 Wails ICNS/ICO，再分别用于运行时 Dock 和调试/正式应用包。尺寸、透明度、资源路径和构建说明见 `assets/brand/README.md`。

“设备导入”沿用相同 token、导航、message 反馈和 Radix 控件。顶部连接条展示实际设备和状态；未连接时给出三步引导与本地存储入口。连接后为素材浏览区 + 300px 规划面板，1150px 以下规划面板进入文档流。浏览区支持列表、网格、时间线，默认网格，三种视图共用 520px 内部滚动空间和 30 项分页。存储与类型使用始终可见的按钮组，选中态沿用语义色；网格与时间线使用 16:10 缩略图卡片，列表仍为 60×44px。页面自然滚动。未适配品牌不显示成可连接设备。

设备缩略图由 device-media-browser.tsx 共用加载和失败回退行为，DNG 缩略图、视频预览与原片明确区分。连接由 device-connection-dialog.tsx 拥有；导入确认、断开和素材预览均使用既有 Dialog。任务进度和记录由 device-import-task-dialog.tsx 在独立弹窗中展示，复用本地导出弹窗的图标、进度和计数样式；标题和操作栏固定，正文内部滚动，记录每页 10 项，文件详情在同一弹窗内展开。开始导入自动打开本次任务；关闭后通过页面顶部“导入任务”重开，页面底部不再展示记录。下载、生成指纹和最终保存分别呈现。全局颜色、字号、圆角和 token 所有者保持既有规则。

## Do's and Don'ts
“诊断日志”复用工作台侧栏、页头、message 反馈和 Select/Button/Input。顶部为本地记录状态、容量与导出操作；正文采用最新优先的事件列表，每次读取 50 条，内部最大滚动高度 560px。时间、级别、模块与中文说明形成固定阅读顺序，协议字段折叠到详情中；窄屏改为单列，保留完整时间和操作。全局字体、颜色和滚动条仍由 styles.css token 拥有，不引入远程资源。

- 保留完整路径的可访问详情，不只依赖 hover。
- 复制操作保留原文件；同名文件自动编号；失败明确展示。
- 禁止浏览器 alert、confirm、prompt。
- 不将演示数据展示成真实扫描结果，不在演示中执行复制。

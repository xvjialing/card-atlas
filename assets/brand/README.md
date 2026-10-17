# CardAtlas 品牌图标

设计意象：相机存储卡中嵌入镜头光圈，搭配金色归档索引标签。森林绿延续工作台主色，米白底板增强深浅桌面背景下的辨识度。

本次使用内置 ImageGen 生成原图，未使用外部设计平台或 API Key。图标只有图形，没有文字，保持小尺寸识别。

## 源文件与输出

- `cardatlas-original.png`：生成原图，1254×1254，带透明通道；这是唯一美术源文件。
- `cardatlas.png`：1024×1024 品牌主图。
- `../../build/appicon.png`：相同的 1024×1024 主图，供 Wails 图标转换与 Go embed 使用。
- `../../build/darwin/icons.icns`：macOS 多尺寸应用图标。
- `../../build/windows/icon.ico`：Windows 多尺寸应用图标。
- `../../frontend/public/cardatlas.png`：256px 侧栏图标。
- `../../frontend/public/favicon.png`：32px 网页标签图标。
- `../../frontend/public/apple-touch-icon.png`：180px 网页收藏图标。

## 自动构建链路

```text
assets/brand/cardatlas-original.png
  → scripts/brand/main.go（Go 标准库，保留透明通道与小尺寸边缘）
  → build/appicon.png / 品牌主图 / 前端图标
  → Wails generate icons
  → macOS ICNS / Windows ICO
```

调试启动和打包均通过 Taskfile 自动调用上述步骤。没有额外图片处理工具依赖；输入不变时不会重复改写 PNG，避免热更新循环。

```sh
./scripts/wails.sh task common:generate:icons
npm run dev
npm run package
```

`main.go` 内嵌 PNG 并通过 Wails `Options.Icon` 设置运行时 Dock 图标，`ActivationPolicyRegular` 保持程序坞入口。调试和正式 `.app` 的 `Info.plist` 都明确引用 `icons.icns`，不保留旧 Wails 模板的 `Assets.car`，避免资源目录优先级覆盖新图标。调试版使用独立的 `local.cardatlas.desktop.dev` 标识。

Dock 是屏幕底部的程序坞。本次实现不添加顶部菜单栏托盘图标，也不修改用户 Dock 偏好。替换应用包后需要退出当前实例再重新启动。

## 生成提示词

以下为内置 ImageGen 使用的完整提示词；实际生成尺寸以文件为准，再由工程标准化导出为 1024px。

```text
Create one finished macOS desktop application icon for CardAtlas, a private local camera memory card photo/video organizer. Use case: production app icon / logo. Square 1024x1024 canvas. A single beautifully crafted restrained geometric brand mark on a warm ivory rounded macOS squircle tile, centered with approximately 80 px clear padding outside the tile. The space outside the tile is genuinely transparent alpha. Inside the tile: a bold deep forest green stylized SD memory card silhouette with a clipped top-right corner; within the card, a warm ivory geometric camera shutter/lens aperture whose opening subtly suggests the letter C, with one small muted brass-gold index-tab accent connecting the idea of an archive to captured photos. Extremely legible at 32 and 64 pixels, clear confident large shapes, thick clean edges, no tiny details, no text, no lettering, no watermark, no multiple icon sheets or presentation mockups. Original identity; do not resemble any existing product logo. Colors coordinated with the app: forest green #28634c, pale sage #e5eee9, warm ivory #f7f6ee, restrained brass #c88745. Mostly flat vector-like precision with very subtle tactile depth, softly beveled ivory tile and a restrained realistic macOS icon shadow; professional photographer's quiet archival tool. Straight-on orthographic composition; keep logo upright, no perspective. Output exactly one final centered icon, ready to use directly in a Dock.
```

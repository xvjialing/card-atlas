# 第三方来源

- [KonradIT/osmosis](https://github.com/KonradIT/osmosis)：MIT，协议记录、Action 6 媒体布局及飞行器 WLM 会话参考；新机型寻址参考 PR #24 的 `9c3d87f59857a982e1e52f7dbb1a2783d05727b3` 提交，保留未验证状态。许可证见 licenses/osmosis.txt。
- [luisreysan/osmo-client](https://github.com/luisreysan/osmo-client)：MIT，DUML、蓝牙握手与数据链路实现参考。许可证见 licenses/osmo-client.txt。
- [Wails](https://github.com/wailsapp/wails)、[TinyGo Bluetooth](https://github.com/tinygo-org/bluetooth)、React、Tailwind CSS、shadcn/ui 和 Radix 的版本锁定在 go.mod/go.sum 与 pnpm-lock.yaml；遵循各自许可证。
- [alpha2go](https://github.com/Knallli/alpha2go) v0.3.0（提交 `02883425c9445c653b2950210187cdd1547320c2`）：Apache-2.0，用于 Sony PTP/IP 握手、会话和素材读取，不调用拍摄、删除或设置写入接口。许可证与原始声明见 `licenses/alpha2go-LICENSE.txt`、`licenses/alpha2go-NOTICE.txt`；相关 Go 扩展库的 BSD 许可证同置于 `licenses/`。

本工程的 Go 协议实现参考上述 MIT 项目重写，保留对应版权与许可。CardAtlas 是独立项目，与 DJI 无隶属或背书关系。

设备连接、DJI 协议、下载与诊断能力迁移自本机 osmo-bridge 工程；通用设备适配、导入规划与任务管理由 CardAtlas 维护。

Sony 无线素材协议的行为研究参考 [ImagingEdge4Linux](https://github.com/schorschii/ImagingEdge4Linux)（GPL-3.0），未复制其实现或引入其代码依赖；本工程按 SOAP/UPnP 数据结构独立实现。产品模式与能力参考 Sony ZV-E10 官方帮助指南和 Monitor+ 官方文档，不代表获得 Sony 或 Monitor+ 的认可。

PTP/IP 路径的研究还参考 [SonyPhotoTransfer](https://github.com/Morning-Sea/SonyPhotoTransfer) 的项目说明，未复制其 Kotlin 实现或引入其 LGPL Java 协议库；实际能力与协议选择以本机 Sony ZV-E10 固件 2.01 的服务描述及真机请求结果为准。

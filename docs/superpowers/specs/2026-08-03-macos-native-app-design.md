# 复利簿 macOS 原生应用设计

## 目标

在现有 Web 项目之外，新增一个只面向 macOS 的 SwiftUI 原生应用。现有 Web 项目继续保留、独立运行；原生应用不启动本地 Web 服务，也不要求用户打开浏览器。

第一阶段的重点是直接读取和写入现有 Web 项目生成的本地 SQLite 数据，使两个客户端可以使用同一份账本数据。

## 项目边界

- 新应用位于与 Web 项目并列的独立目录 `Compound-interest-macOS/`。
- Web 项目目录和代码不迁移、不删除、不重构。
- 原生应用启动后直接显示 SwiftUI 主窗口。
- 首次启动通过目录选择器让用户选择 Web 项目目录；之后持久化该目录书签。
- 选择的目录中没有可识别数据库时，提供创建新账本的入口，但不覆盖或修改已有文件。

## 技术方案

- UI：SwiftUI，目标为 macOS 13 或更高版本。
- 数据访问：GRDB.swift，读写 SQLite。
- 网络：`URLSession` 请求公开行情和汇率接口。
- 计算：在 Swift 中实现与 `app/portfolio.ts` 等价的资产汇总、复利预测、定投和快照逻辑。
- 本地设置：使用 `UserDefaults` 保存安全书签、当前账本和当前用户选择。

## 数据库兼容与定位

Web 项目的本地 D1 文件位于：

`.wrangler/state/v3/d1/miniflare-D1DatabaseObject/`

数据库文件名由运行时生成，不能硬编码。原生应用启动时扫描该目录中的 SQLite 文件，并通过 SQLite header、可访问性和表结构校验识别目标文件。至少要求存在以下表：

- `users`
- `assets`
- `exchange_rates`
- `asset_history`

`sessions` 表继续保留给 Web 项目使用；原生应用不依赖 Cookie 或 HTTP session，而是在本机选择账本用户。

现有字段和单位保持不变：资产金额按分存储，`annual_rate` 使用现有百分比语义，汇率存储为人民币兑换比率。未知字段和未知表不得被自动删除或重写。

## 应用结构

```text
Compound-interest-macOS/
├── Package.swift
├── Sources/CompoundInterest/
│   ├── App/
│   ├── Models/
│   ├── Database/
│   ├── Services/
│   ├── Calculations/
│   └── Views/
└── Tests/CompoundInterestTests/
```

主要职责：

- `Database`：数据库定位、GRDB 连接、迁移兼容检查、备份和文件锁。
- `Models`：对应现有表的 Codable/FetchableRecord 模型。
- `Services`：行情、汇率、账本和用户选择服务。
- `Calculations`：总资产、配置比例、历史快照和复利预测。
- `Views`：侧边栏、总览、资产列表、编辑表单、历史走势和设置。

## 界面与数据流

主窗口采用侧边栏导航：

- **总览**：总资产、资产配置、预期收益、预测曲线。
- **资产**：筛选、添加、编辑、删除、行情同步。
- **历史**：按日查看资产快照和趋势。
- **设置**：账本目录、用户、数据库备份和网络状态。

总览和资产操作直接调用本地数据库服务，不经过 HTTP API。股票、基金和外汇操作通过 `URLSession` 获取数据；网络失败时保留已有收益率和汇率，并显示可恢复的状态提示。资产新增、修改、删除和汇率刷新完成后，按现有规则更新当天唯一的 `asset_history` 快照。

## 并发、备份与错误处理

- 每次写操作前创建带时间戳的数据库副本；备份失败则阻止写入。
- 使用进程内串行写入队列和 SQLite busy timeout；检测到文件被其他进程占用时提示用户关闭 Web 服务或稍后重试。
- 数据库结构不兼容时以只读模式打开，并提示用户备份和升级，不自动修改未知结构。
- 找不到数据库时引导用户重新选择项目目录或创建新账本。
- 无网络时仍可查看本地资产、历史和手动利率预测。

## 测试与验收

- 数据库目录扫描、哈希文件识别和表结构校验。
- 使用现有 `.wrangler/state` 数据读取用户、资产、汇率和历史记录。
- 新增、修改、删除资产及每日快照唯一性。
- Swift 复利预测与现有 `app/portfolio.ts` 的固定样例对照。
- 网络失败、数据库缺失、数据库锁定、备份失败等错误场景。
- macOS 手动验收：首次选择目录、重启恢复、Web 与原生应用交替读写、备份恢复。

## 非目标

- 第一阶段不支持 Windows 或 Linux。
- 不把 Web 页面嵌入原生应用，不引入 Electron、Tauri 或本地 HTTP 服务。
- 不改变现有 Web API、认证协议或数据库表结构。
- 不在第一阶段增加云同步、iCloud 同步或多设备协作。

# 复利簿 macOS 原生应用实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 在 `Compound-interest-macOS/` 中创建不依赖浏览器和本地 HTTP 服务的 SwiftUI macOS 应用，直接读写现有 Web 项目的 D1 SQLite 数据。

**架构：** SwiftUI 负责原生窗口和导航；GRDB 管理 SQLite 连接、查询、事务和记录模型；本地服务层隔离数据库、行情、汇率和复利计算；视图模型通过 Observation 将服务结果提供给界面。应用首次启动扫描用户选择的 Web 项目目录，定位并校验 `.wrangler/state` 下的数据库。

**技术栈：** Swift 5.9、macOS 13+、SwiftUI、GRDB.swift、Foundation URLSession、Swift Testing/XCTest。

---

## 文件清单

计划中的新文件全部位于独立目录，不修改现有 Web 实现：

- 创建：`Compound-interest-macOS/Package.swift`，声明 macOS 目标、GRDB 依赖和测试目标。
- 创建：`Compound-interest-macOS/Sources/CompoundInterest/App/CompoundInterestApp.swift`，应用入口与窗口。
- 创建：`Compound-interest-macOS/Sources/CompoundInterest/Models/DatabaseModels.swift`，对应 `users`、`assets`、`exchange_rates`、`asset_history` 的 GRDB 模型。
- 创建：`Compound-interest-macOS/Sources/CompoundInterest/Database/DatabaseLocator.swift`，目录扫描、SQLite 表校验和安全书签路径。
- 创建：`Compound-interest-macOS/Sources/CompoundInterest/Database/DatabaseManager.swift`，GRDB 队列、事务、备份、busy timeout 和只读降级。
- 创建：`Compound-interest-macOS/Sources/CompoundInterest/Services/AssetRepository.swift`，用户、资产、汇率和历史 CRUD。
- 创建：`Compound-interest-macOS/Sources/CompoundInterest/Calculations/PortfolioCalculator.swift`，复用 TypeScript 计算语义。
- 创建：`Compound-interest-macOS/Sources/CompoundInterest/Services/MarketService.swift` 和 `ExchangeRateService.swift`，行情和外汇 HTTP 客户端。
- 创建：`Compound-interest-macOS/Sources/CompoundInterest/App/AppModel.swift`，启动状态、当前用户、资产刷新和错误状态。
- 创建：`Compound-interest-macOS/Sources/CompoundInterest/Views/ContentView.swift` 及总览、资产、历史、设置子视图。
- 创建：`Compound-interest-macOS/Tests/CompoundInterestTests/*Tests.swift`，单元、数据库集成和服务失败测试。
- 创建：`Compound-interest-macOS/scripts/build-app.sh`，将 SwiftPM 可执行文件包装为 `.app`，并提供本地运行命令。

### 任务 1：初始化 SwiftPM 原生项目

**文件：** `Compound-interest-macOS/Package.swift`、`Sources/.../App/CompoundInterestApp.swift`

- [ ] **步骤 1：编写启动测试**

在 `Tests/CompoundInterestTests/AppLaunchTests.swift` 中验证目标编译并暴露 `CompoundInterestApp`，测试只检查 `Package.swift` 的 macOS 目标可解析。

- [ ] **步骤 2：运行测试确认当前失败**

运行：`cd Compound-interest-macOS && swift test`

预期：因目录和 Package manifest 尚不存在而失败。

- [ ] **步骤 3：创建 SwiftPM manifest 和最小窗口**

声明 macOS 13 平台、`GRDB` 产品依赖、应用 executable target 与测试 target；入口使用 `@main`、`WindowGroup` 和一个显示应用名称的 `ContentView`。

- [ ] **步骤 4：运行测试确认通过**

运行：`cd Compound-interest-macOS && swift test`

预期：PASS，且 `swift build` 成功。

- [ ] **步骤 5：Commit**

```bash
git add Compound-interest-macOS/Package.swift Compound-interest-macOS/Sources Compound-interest-macOS/Tests
git commit -m "feat: 初始化 macOS 原生应用"
```

### 任务 2：实现数据库定位、模型和安全连接

**文件：** `DatabaseModels.swift`、`DatabaseLocator.swift`、`DatabaseManager.swift` 及对应测试。

- [ ] **步骤 1：编写失败测试**

测试临时目录中的 `*.sqlite` 文件：只接受 SQLite header 正确且包含四张业务表的文件；忽略 `metadata.sqlite`、WAL 文件和不完整数据库；验证扫描到多个候选时按最近修改时间选择并返回候选列表。

- [ ] **步骤 2：运行测试确认失败**

运行：`cd Compound-interest-macOS && swift test --filter DatabaseLocatorTests`

预期：类型和定位器尚不存在，测试编译失败。

- [ ] **步骤 3：实现模型和定位/连接**

让模型实现 `FetchableRecord`、`PersistableRecord` 和 `TableRecord`，明确 snake_case 列名。定位器扫描用户选择目录下的 `.wrangler/state/v3/d1/miniflare-D1DatabaseObject`，读取 SQLite header 后用 SQLite schema 查询校验表。`DatabaseManager` 使用 `DatabaseQueue`，设置 busy timeout，提供 `read`、`write`、`backup` 和只读打开方法。

- [ ] **步骤 4：运行测试确认通过**

运行：`cd Compound-interest-macOS && swift test --filter DatabaseLocatorTests`

预期：PASS，错误文件不会被选中，数据库连接可读。

- [ ] **步骤 5：Commit**

```bash
git add Compound-interest-macOS/Sources/CompoundInterest/Models Compound-interest-macOS/Sources/CompoundInterest/Database Compound-interest-macOS/Tests
git commit -m "feat: 接入现有 SQLite 账本"
```

### 任务 3：实现本地仓储和快照事务

**文件：** `Services/AssetRepository.swift`、`DatabaseManager.swift`、仓储测试。

- [ ] **步骤 1：编写失败测试**

使用临时 SQLite 数据库验证按 `user_id` 读取资产、资产 CRUD、汇率 upsert、历史按 `(user_id, snapshot_date)` 更新，以及金额不完整时不写入快照。

- [ ] **步骤 2：运行测试确认失败**

运行：`cd Compound-interest-macOS && swift test --filter AssetRepositoryTests`

预期：仓储 API 尚不存在而失败。

- [ ] **步骤 3：实现仓储**

提供 `users()`、`assets(for:)`、`createAsset`、`updateAsset`、`deleteAsset`、`exchangeRates()`、`saveExchangeRates`、`history(for:)` 和 `recordDailySnapshot`。所有写操作先调用备份，再在同一事务中完成资产变更和快照更新；不实现 Web session。

- [ ] **步骤 4：运行测试确认通过**

运行：`cd Compound-interest-macOS && swift test --filter AssetRepositoryTests`

预期：PASS，重复当天快照只保留一条最新记录。

- [ ] **步骤 5：Commit**

```bash
git add Compound-interest-macOS/Sources/CompoundInterest/Services/AssetRepository.swift Compound-interest-macOS/Sources/CompoundInterest/Database Compound-interest-macOS/Tests
git commit -m "feat: 添加资产仓储和历史快照"
```

### 任务 4：迁移复利计算并接入网络服务

**文件：** `Calculations/PortfolioCalculator.swift`、`Services/MarketService.swift`、`Services/ExchangeRateService.swift` 及测试。

- [ ] **步骤 1：编写失败测试**

固定包含人民币、外币、固定资产、基金定投和美股代码的资产样例，验证总资产、预测值、预期收益、加权收益率和交易日定投结果与 `app/portfolio.ts` 的结果一致；另测网络非 2xx 和 JSON 缺字段时返回可展示错误。

- [ ] **步骤 2：运行测试确认失败**

运行：`cd Compound-interest-macOS && swift test --filter PortfolioCalculatorTests`

预期：Swift 计算器和服务类型尚不存在而失败。

- [ ] **步骤 3：实现等价计算和客户端**

按现有年化百分比、货币汇率、固定资产零收益、上海/纽约时区交易日规则实现计算器。网络服务使用 `URLSession`、明确超时和 Codable 响应模型，保留手动收益率作为失败回退。

- [ ] **步骤 4：运行测试确认通过**

运行：`cd Compound-interest-macOS && swift test --filter PortfolioCalculatorTests`

预期：PASS，固定样例误差不超过 1 分钱；网络错误被转换为领域错误。

- [ ] **步骤 5：Commit**

```bash
git add Compound-interest-macOS/Sources/CompoundInterest/Calculations Compound-interest-macOS/Sources/CompoundInterest/Services Compound-interest-macOS/Tests
git commit -m "feat: 添加原生复利计算和网络服务"
```

### 任务 5：建立应用状态和 SwiftUI 工作流

**文件：** `AppModel.swift`、`Views/ContentView.swift`、总览/资产/历史/设置视图及预览数据。

- [ ] **步骤 1：编写状态和视图测试**

测试 `AppModel` 在无目录、目录扫描中、数据库就绪、只读和网络失败状态间转换；测试资产保存后刷新资产和历史发布值。

- [ ] **步骤 2：运行测试确认失败**

运行：`cd Compound-interest-macOS && swift test --filter AppModelTests`

预期：应用状态和视图模型尚不存在而失败。

- [ ] **步骤 3：实现原生界面**

使用 `NavigationSplitView` 和 macOS 工具栏。首次启动显示目录选择；就绪后显示总览、资产列表筛选、添加/编辑表单、删除确认、历史图表和设置。所有按钮调用 `AppModel` 的 async 方法，展示加载、空状态、错误和网络过期状态。

- [ ] **步骤 4：运行测试和构建**

运行：`cd Compound-interest-macOS && swift test --filter AppModelTests && swift build`

预期：状态测试和完整构建均通过。

- [ ] **步骤 5：Commit**

```bash
git add Compound-interest-macOS/Sources/CompoundInterest/App Compound-interest-macOS/Sources/CompoundInterest/Views Compound-interest-macOS/Tests
git commit -m "feat: 构建 SwiftUI 资产工作台"
```

### 任务 6：打包、备份恢复和端到端验收

**文件：** `scripts/build-app.sh`、`README.md`（仅新增 macOS 应用说明）、端到端测试。

- [ ] **步骤 1：编写打包和恢复测试**

验证备份目录创建、备份文件可被 SQLite 打开、恢复后资产数量和历史记录一致；验证脚本在 macOS 目标架构上生成 `.app/Contents/MacOS/CompoundInterest`。

- [ ] **步骤 2：实现打包脚本**

脚本执行 `swift build -c release`，创建 `.app` bundle、`Info.plist` 和 `Contents/MacOS`，并支持 `open CompoundInterest.app`。脚本不启动 Web 服务。

- [ ] **步骤 3：运行完整验证**

运行：`cd Compound-interest-macOS && swift test && ./scripts/build-app.sh && open ./build/CompoundInterest.app`

预期：测试通过，应用打开原生窗口；选择当前 Web 项目目录后能读取同一账本。

- [ ] **步骤 4：更新文档**

在现有 README 中追加独立的 macOS 构建、首次选择 Web 项目目录、备份位置、同时运行 Web 与原生应用的写入注意事项。

- [ ] **步骤 5：Commit**

```bash
git add Compound-interest-macOS/scripts Compound-interest-macOS/README.md README.md
git commit -m "feat: 添加 macOS 应用打包和使用说明"
```

## 计划自检

- 规格中的目标、项目边界、数据库定位、数据流、错误处理、备份并发和验收范围均有对应任务。
- 已扫描计划文本，没有占位符或未定义的泛化步骤。
- 前后使用的目录、模型、仓储方法和测试过滤器名称保持一致。
- 每个任务先测试、再实现、再验证并独立提交，未要求修改现有 Web 源码。

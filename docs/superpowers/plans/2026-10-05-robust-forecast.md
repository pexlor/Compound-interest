# 稳健资产预测 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. 用户已授权执行，本次在独立工作区内直接实施。

**Goal:** 统一资产与退休预测，提供稳健收益、联合抽样范围和滚动回测。
**Architecture:** Go service 纯计算引擎，数据库装配快照，HTTP异步预热缓存，React展示同一响应。
**Tech Stack:** Go 1.24 / SQLite / React / TypeScript，无新依赖。
**Spec:** docs/superpowers/specs/2026-10-05-robust-forecast-design.md

## Global Constraints
- 金额响应使用分，日期使用上海日期；新增函数和结构体中文注释。
- 5000路径、3月连续片段、最多10年训练、至少60月基准和36月共同样本。
- 不写入持仓估值；新增资金只计本金；固定资产不参与退休可用资产。
- 默认通胀2%、期限10年、公积金不计退休可用资产；情景可改。
- 用户原有改动不提交、不覆盖；完成后合并main，不自动部署。

## Review Focus
- 缺数据/日期缺口必须不可用，不得当零收益。
- 外币到账和资产都按同一路径汇率，人民币收益不得当美元收益。
- 月末、周年和现金日期分段不能双计收益或本金。
- 旧账号/旧情景请求不得更新新界面。
- 回测训练不得包含未来样本，短历史不能凭空生成精度指标。

### Task 1: 总收益与月度特征
**Files:** Create service/forecast_history.go、forecast_history_test.go; Modify httpapi/market_cache.go。
**Interfaces:** PriceObservation、MonthlyReturns、FundTotalReturn、EstimateDrift供后续计算使用。
- [ ] 写分红、区间前分红、日期缺口、短样本、未来截断及超额收益折减失败测试。
- [ ] 运行go test ./internal/service，Expected: 新函数未定义。
- [ ] 实现特征；历史基金年化复用纠正后的总收益序列。
- [ ] 运行go test ./internal/service ./internal/httpapi，Expected: pass。
- [ ] 提交feat: 修正总收益并增加稳健月度收益估计。

### Task 2: 联合模拟引擎与回测
**Files:** Create service/forecast.go、forecast_test.go、forecast_backtest.go及测试。
**Interfaces:** ForecastInput、ForecastOptions、ForecastResult、SimulateForecast、BacktestForecast供数据库/API复用。
- [ ] 写逐项复利、到账不复利、完美相关性、汇率、流动性、通胀、确定随机种子和无前视回测失败测试。
- [ ] 运行go test ./internal/service，Expected: 新引擎未定义。
- [ ] 实现联合块抽样、日期分段、量化范围及回测。
- [ ] 运行go test ./internal/service，Expected: pass。
- [ ] 提交feat: 实现联合历史抽样预测及滚动回测。

### Task 3: 数据装配、接口和缓存预热
**Files:** Create service/forecast_store.go、httpapi/forecast.go及测试、cmd/forecast-backtest/main.go; Modify handlers.go、ledger.go、retirement.go、market_jobs.go。
**Interfaces:** ForecastFor(q,userID,options,asOf) 返回用户隔离快照计算，ForecastSecurities列出所需行情。
- [ ] 写未登录、用户隔离、缺目标汇率、基准未选、完整缓存和退休同口径失败测试。
- [ ] 运行go test ./internal/httpapi，Expected: 新接口404。
- [ ] 实现读取事务、后台预热、统一退休入口和回测CLI。
- [ ] 运行go test ./...，Expected: pass。
- [ ] 提交feat: 接入统一预测接口与基准缓存。

### Task 4: 前端展示与验证
**Files:** Create frontend/src/forecast.ts、ForecastPanel.tsx及tests/forecast.test.mjs; Modify Dashboard.tsx、globals.css、README.md。
**Interfaces:** ForecastResponse与后端JSON一致；情景请求仅当前身份/输入可应用。
- [ ] 写状态、单位、区间、配置和迟到响应失败测试。
- [ ] 运行前端node tests，Expected: 新模块不存在。
- [ ] 实现范围/概率/通胀/基准选择、当前月分项和统一退休；历史年化只展示。
- [ ] 运行前端全测试和build，Go test/vet/race；Expected: pass。
- [ ] 独立审查，修复重要问题，记录真实回测可用性。
- [ ] 提交feat: 展示预测区间与退休达标概率；合并main。

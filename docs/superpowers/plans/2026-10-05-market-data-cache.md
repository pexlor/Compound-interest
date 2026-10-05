# Market Data Cache Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Persist daily market observations and daily 1/3/5/10-year returns, fill missing history, and refresh at Shanghai 09:30 and 17:00.
**Architecture:** A per-database cache coordinator shares history and in-flight work across HTTP and scheduler instances. Providers expose validated daily series; SQLite stores observations, coverage, quotes, derived results and jobs. Dashboard includes local cached results without network work.
**Tech Stack:** Go, SQLite, React/TypeScript, existing upstream providers.
**Spec:** docs/superpowers/specs/2026-10-05-market-data-cache-design.md

## Global Constraints
- Fixed Asia/Shanghai scheduling at 09:30 and 17:00; preserve existing snapshot/rate jobs.
- Upsert daily source observations; distinguish observation dates and fetch timestamps.
- Never persist failed/truncated history as complete or invent nontrading-day prices.
- Shared security data, isolated user snapshots; longest active range 3650 days.
- Preserve future-contributions-as-principal forecast policy and existing API contracts.
- Additive SQLite migrations; new tables included in optional MySQL backup registry.

## Review Focus
- New assets during a running refresh must be fetched on subsequent reads.
- A partial fund pagination response must leave coverage incomplete.
- Stale cache errors must not replace a valid rate with zero.
- Updating adjusted historical prices must invalidate every affected derived interval.
- A reboot after both job slots must coalesce missed work without losing job state.

### Task 1: Persistent schema and backup support
**Files:** internal/database/market_cache.go, sqlite.go, backup_tables.go, backup_mysql.go, backup_mysql_schema.go, market_cache_test.go.
**Interfaces:** Open creates new cache tables without mutating holdings; registry includes all new tables with composite primary keys where applicable.
- [x] Write migration/reopen/upsert tests; run `go test ./internal/database` and observe missing tables.
- [x] Create daily prices, quote, coverage, derived metadata, asset day snapshots and job tables. Use a separate metadata table to extend market_returns without altering its historical contract.
- [x] Verify schema/backup tests and commit.

### Task 2: Daily source collection and return calculation
**Files:** internal/httpapi/market_sources.go, market_sources_test.go, market.go.
**Interfaces:** fetchMarketSeries(ctx, client, category, code, from) returns daily observations, quote and confirmed coverage; cached historical FX supplies valid endpoint conversions.
- [x] Test fund multi-page coverage and interrupted pagination, Yahoo adjusted closes, Tencent range pagination and currency endpoint handling.
- [x] Implement bounded/cancellable provider requests, complete history range ingestion, daily FX persistence and annualized calculations preserving current 365.25-day behavior.
- [x] Verify provider tests and commit.

### Task 3: Cache read-through and HTTP integration
**Files:** internal/httpapi/market_cache.go, market_cache_test.go, market.go, ledger_handlers.go, rates.go.
**Interfaces:** readMarketCache(category, code, days) reads without network; cachedMarket(ctx, category, code, days, force) deduplicates synchronization, returns stale immediately or pending for first fill; dashboard returns marketResults for 1095 days.
- [x] Test hit without network, missing fill, cross-account deduplication, stale fallback/cooldown, forced invalidation and first dashboard results.
- [x] Persist ingestion atomically, recalculate four intervals from one series, coalesce requests, bound concurrency and cancellation, refresh checks every 30 minutes, 30-day overlap and weekly full validation.
- [x] Verify HTTP suite and commit.

### Task 4: Scheduled refresh and private snapshots
**Files:** internal/httpapi/market_jobs.go, scheduler.go, market_jobs_test.go.
**Interfaces:** refreshMarketBatch(ctx, force) deduplicates active codes, stores daily snapshots; recoverMarketJobs(ctx, now) resumes missed slots; nextMarketRun(now) returns 09:30/17:00.
- [x] Test slot boundaries, restart coalescing, failure recovery and user snapshot isolation.
- [x] Integrate cancellable scheduling and persisted run status; retain existing 05:00 and 09:16 jobs.
- [x] Verify full Go suite and commit.

### Task 5: First-paint cached rates and pending UI
**Files:** frontend/src/market-cache.ts, Dashboard.tsx, frontend/tests/market-cache.test.mjs.
**Interfaces:** market cache helpers merge only matching intervals and report pending market assets; Dashboard seeds rates from dashboard and polls pending fills with cancellation.
- [x] Test missing rate cannot silently become zero, stale valid rate retained, interval mismatch excluded.
- [x] Seed first-paint results, mark prediction loading when inputs missing, display data dates, retry pending every 3 seconds, forced refresh parameter.
- [x] Run npm test and npm run build; commit only task changes, preserving pre-existing Dashboard edits.

### Task 6: Review, production build and deployment
**Files:** deployment artifacts and execution ledger.
- [x] Run Go tests/race checks, frontend tests/build, diff checks and fresh whole-change review.
- [x] Build static ARM64 backend and frontend using existing deployment Dockerfile.
- [x] Back up stopped SQLite service, stage new release, atomically activate and restart as nobody:fulibu.
- [x] Verify health, authenticated cache counts/dates, repeated local reads, scheduler state, unchanged holdings and page behavior. Record material source limitations.

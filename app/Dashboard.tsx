"use client";

import { FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import { calculatePortfolio } from "./portfolio";

type Category = "stock" | "fund" | "money" | "deposit" | "housing" | "fixed";
type Currency = "CNY" | "USD" | "HKD" | "EUR" | "JPY" | "GBP" | "SGD" | "AUD" | "CAD" | "CHF";
type MarketReturnMeta = {
  annualRate: number;
  requestedDays: number;
  actualDays: number;
  historyLimited: boolean;
  startDate: string;
  endDate: string;
  calculationDate: string;
  stale?: boolean;
  currentPrice?: number;
  priceCurrency?: Currency;
  priceDate?: string;
};
type Asset = {
  id: number;
  name: string;
  category: Category;
  code: string | null;
  amount: number;
  quantity: number | null;
  currency: Currency;
  annual_rate: number;
  investment_strategy?: "none" | "monthly" | "weekly" | "yearly" | "daily" | null;
  investment_amount?: number | null;
  note: string;
  created_at: string;
  market_return?: MarketReturnMeta;
};
type User = { id: number; email: string; displayName: string };
type HistoryEntry = {
  id: number;
  snapshot_date: string;
  total_cny: number;
  trigger: "asset_change" | "exchange_refresh" | "scheduled_daily";
  rate_date: string | null;
};

const categoryMeta: Record<Category, { name: string; short: string; color: string }> = {
  stock: { name: "股票", short: "股", color: "#ee6a4d" },
  fund: { name: "基金", short: "基", color: "#a78bfa" },
  money: { name: "货币基金", short: "货", color: "#28a88a" },
  deposit: { name: "存款", short: "存", color: "#e7b344" },
  housing: { name: "公积金", short: "积", color: "#5196e3" },
  fixed: { name: "固定资产", short: "固", color: "#8c98a4" },
};

const currencyMeta: Record<Currency, string> = {
  CNY: "人民币",
  USD: "美元",
  HKD: "港币",
  EUR: "欧元",
  JPY: "日元",
  GBP: "英镑",
  SGD: "新加坡元",
  AUD: "澳元",
  CAD: "加拿大元",
  CHF: "瑞士法郎",
};

const money = (cents: number, digits = 0) =>
  new Intl.NumberFormat("zh-CN", {
    style: "currency",
    currency: "CNY",
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  }).format(cents / 100);

const originalMoney = (cents: number, currency: Currency) =>
  new Intl.NumberFormat("zh-CN", {
    style: "currency",
    currency,
    currencyDisplay: "symbol",
    minimumFractionDigits: currency === "JPY" ? 0 : 2,
    maximumFractionDigits: currency === "JPY" ? 0 : 2,
  }).format(cents / 100);

const toCny = (asset: Asset, rates: Partial<Record<Currency, number>>) =>
  asset.amount * (rates[asset.currency] ?? 0);

const isQuantityAsset = (asset: Pick<Asset, "category">) => asset.category === "stock" || asset.category === "fund";

const quantityText = (value: number) => new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 8 }).format(value);

const priceText = (value: number, currency: Currency) => new Intl.NumberFormat("zh-CN", {
  style: "currency", currency, maximumFractionDigits: 4,
}).format(value);

const supportsInvestment = (asset: Pick<Asset, "category">) => asset.category === "fund";

const marketKey = (category: string, code: string) => `${category}:${code.trim().toUpperCase()}`;

export default function Dashboard() {
  const [user, setUser] = useState<User | null>(null);
  const [authLoading, setAuthLoading] = useState(true);
  const [assetsLoading, setAssetsLoading] = useState(false);
  const [assets, setAssets] = useState<Asset[]>([]);
  const [marketRates, setMarketRates] = useState<Record<string, MarketReturnMeta>>({});
  const [history, setHistory] = useState<HistoryEntry[]>([]);
  const [activeFilter, setActiveFilter] = useState<"all" | Category>("all");
  const [horizon, setHorizon] = useState(3);
  const [lookback, setLookback] = useState(3);
  const [modalOpen, setModalOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [updating, setUpdating] = useState(false);
  const [toast, setToast] = useState("");
  const [selected, setSelected] = useState<Asset | null>(null);
  const [exchangeRates, setExchangeRates] = useState<Partial<Record<Currency, number>>>({ CNY: 1 });
  const [exchangeDate, setExchangeDate] = useState("");
  const [exchangeLoading, setExchangeLoading] = useState(false);
  const [exchangeStale, setExchangeStale] = useState(false);

  const loadMarketRates = useCallback(async (selectedLookback: number, notify = false, signal?: AbortSignal) => {
    setSyncing(true);
    try {
      const response = await fetch(`/api/market?days=${selectedLookback * 365}`, { signal });
      const data = await response.json() as { results?: Array<MarketReturnMeta & { category: string; code: string }>; errors?: unknown[]; error?: string };
      if (response.status === 401) {
        setUser(null);
        return;
      }
      if (!response.ok) throw new Error(data.error || "读取市场收益失败");
      const next = Object.fromEntries((data.results ?? []).map((result) => [
        marketKey(result.category, result.code),
        result,
      ]));
      setMarketRates(next);
      if (notify) {
        const failed = data.errors?.length ?? 0;
        setToast(failed ? `已读取 ${data.results?.length ?? 0} 项，${failed} 项行情暂不可用` : `已读取 ${data.results?.length ?? 0} 项最新价格与收益率`);
      }
    } catch (error) {
      if (error instanceof DOMException && error.name === "AbortError") return;
      if (notify) setToast(error instanceof Error ? error.message : "读取市场收益失败");
    } finally {
      if (!signal?.aborted) setSyncing(false);
    }
  }, []);

  useEffect(() => {
    fetch("/api/auth/me")
      .then(async (response) => response.ok ? (await response.json()).user : null)
      .then((currentUser) => {
        setUser(currentUser);
        setAssetsLoading(Boolean(currentUser));
      })
      .catch(() => setUser(null))
      .finally(() => setAuthLoading(false));
  }, []);

  useEffect(() => {
    if (!user) return;
    Promise.all([
      fetch("/api/assets").then(async (response) => {
        if (response.status === 401) throw new Error("unauthorized");
        if (!response.ok) throw new Error("assets");
        return response.json();
      }),
      fetch("/api/exchange-rates").then(async (response) => {
        if (!response.ok) throw new Error("rates");
        return response.json();
      }).catch(() => null),
      fetch("/api/history?limit=3650").then(async (response) => {
        if (response.status === 401) throw new Error("unauthorized");
        if (!response.ok) throw new Error("history");
        return response.json();
      }),
    ])
      .then(([assetData, rateData, historyData]) => {
        setAssets(assetData.assets ?? []);
        setHistory(historyData.history ?? []);
        if (rateData) {
          setExchangeRates(rateData.rates);
          setExchangeDate(rateData.date);
          setExchangeStale(Boolean(rateData.stale));
        } else {
          setToast("资产已读取，但最新汇率暂不可用");
        }
      })
      .catch((error) => {
        if (error instanceof Error && error.message === "unauthorized") setUser(null);
        else setToast("读取本地资产失败，请刷新重试");
      })
      .finally(() => {
        setAssetsLoading(false);
        setExchangeLoading(false);
      });
  }, [user]);

  useEffect(() => {
    if (!user) return;
    const controller = new AbortController();
    const timer = window.setTimeout(() => void loadMarketRates(lookback, false, controller.signal), 0);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [loadMarketRates, lookback, user]);

  useEffect(() => {
    if (!toast) return;
    const timer = window.setTimeout(() => setToast(""), 2800);
    return () => window.clearTimeout(timer);
  }, [toast]);

  const displayAssets = useMemo(() => assets.map((asset) => {
    if (!asset.code) return asset;
    const marketReturn = marketRates[marketKey(asset.category, asset.code)];
    if (!marketReturn) return asset;
    const liveAmount = asset.quantity && marketReturn.currentPrice
      ? Math.round(asset.quantity * marketReturn.currentPrice * 100)
      : asset.amount;
    return {
      ...asset,
      amount: liveAmount,
      currency: marketReturn.priceCurrency ?? asset.currency,
      annual_rate: marketReturn.annualRate,
      market_return: marketReturn,
    };
  }), [assets, marketRates]);
  const missingExchangeRate = displayAssets.some((asset) => !exchangeRates[asset.currency]
    || isQuantityAsset(asset) && Boolean(asset.quantity) && !asset.market_return?.currentPrice);
  const portfolio = useMemo(() => calculatePortfolio(displayAssets, exchangeRates, horizon), [displayAssets, exchangeRates, horizon]);
  const total = portfolio?.total ?? 0;
  const forecast = portfolio?.forecast ?? 0;
  const expectedGain = portfolio?.expectedGain ?? 0;
  const weightedRate = portfolio?.weightedRate ?? 0;

  const grouped = useMemo(() => {
    return (Object.keys(categoryMeta) as Category[]).map((category) => ({
      category,
      amount: displayAssets.filter((item) => item.category === category).reduce((sum, item) => sum + toCny(item, exchangeRates), 0),
    })).filter((item) => item.amount > 0);
  }, [displayAssets, exchangeRates]);

  const filtered = activeFilter === "all" ? displayAssets : displayAssets.filter((asset) => asset.category === activeFilter);
  const limitedHistoryCount = displayAssets.filter((asset) => asset.market_return?.historyLimited).length;
  const selectedMarket = selected ? displayAssets.find((asset) => asset.id === selected.id) ?? selected : null;
  const chartValues = Array.from({ length: horizon + 1 }, (_, index) => calculatePortfolio(displayAssets, exchangeRates, index)?.forecast ?? 0);
  const minChart = Math.min(...chartValues);
  const maxChart = Math.max(...chartValues);

  async function refreshHistory() {
    try {
      const response = await fetch("/api/history?limit=3650");
      if (response.status === 401) {
        setUser(null);
        return;
      }
      if (!response.ok) throw new Error("history");
      const data = await response.json();
      setHistory(data.history ?? []);
    } catch {
      setToast("资产已保存，但历史走势读取失败");
    }
  }

  async function addAsset(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSaving(true);
    try {
      const form = new FormData(event.currentTarget);
      const category = form.get("category") as Category;
      let rate = Number(form.get("annualRate")) || 0;
      let marketReturn: MarketReturnMeta | null = null;
      const code = String(form.get("code") || "").trim().toUpperCase();
      const quantityAsset = category === "stock" || category === "fund";
      const quantity = quantityAsset ? Number(form.get("quantity")) : null;
      if (code && ["stock", "fund", "money"].includes(category)) {
        const marketResponse = await fetch(`/api/market?code=${encodeURIComponent(code)}&category=${category}&days=${lookback * 365}`);
        const market = await marketResponse.json();
        if (!marketResponse.ok && quantityAsset) throw new Error(market.error || "暂时无法读取实时价格");
        if (marketResponse.ok) {
          rate = Number(market.annualRate.toFixed(2));
          marketReturn = market as MarketReturnMeta;
        }
      }
      if (quantityAsset && (!marketReturn?.currentPrice || !marketReturn.priceCurrency)) throw new Error("暂时无法读取实时价格，请稍后重试");
      const inputAmount = quantityAsset ? quantity! * marketReturn!.currentPrice! : Number(form.get("amount"));
      const inputCurrency = quantityAsset ? marketReturn!.priceCurrency! : form.get("currency");
      const response = await fetch("/api/assets", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: form.get("name"), category, code,
          amount: inputAmount, quantity, currency: inputCurrency, annualRate: rate, note: form.get("note"),
          investmentStrategy: form.get("investmentStrategy") || "none", investmentAmount: Number(form.get("investmentAmount")) || undefined,
        }),
      });
      const data = await response.json();
      if (response.status === 401) {
        setUser(null);
        return;
      }
      if (!response.ok) return setToast(data.error || "保存失败，请重试");
      setAssets((current) => [...current, data.asset]);
      if (marketReturn) setMarketRates((current) => ({ ...current, [marketKey(category, code)]: marketReturn! }));
      await refreshHistory();
      setModalOpen(false);
      setToast(data.snapshot ? "资产已加入总览，今日历史已更新" : "资产已加入；汇率不完整，今日历史暂未更新");
    } catch (error) {
      setToast(error instanceof Error ? error.message : "保存失败，请检查本地服务后重试");
    } finally {
      setSaving(false);
    }
  }

  async function syncMarketRates() {
    await loadMarketRates(lookback, true);
  }

  async function refreshExchangeRates() {
    setExchangeLoading(true);
    try {
      const response = await fetch("/api/exchange-rates?refresh=1");
      const data = await response.json();
      if (!response.ok) throw new Error(data.error || "读取最新汇率失败");
      setExchangeRates(data.rates);
      setExchangeDate(data.date);
      setExchangeStale(Boolean(data.stale));
      await refreshHistory();
      setToast(data.stale ? "实时汇率暂不可用，已继续使用上次汇率" : data.snapshot ? "最新汇率与今日历史已更新" : "最新汇率已更新，历史快照暂不可用");
    } catch (error) {
      setToast(error instanceof Error ? error.message : "读取最新汇率失败");
    } finally {
      setExchangeLoading(false);
    }
  }

  async function removeAsset(asset: Asset) {
    const response = await fetch(`/api/assets?id=${asset.id}`, { method: "DELETE" });
    if (response.status === 401) {
      setUser(null);
      return;
    }
    const data = await response.json();
    if (!response.ok) return setToast(data.error || "删除失败，请重试");
    setAssets((current) => current.filter((item) => item.id !== asset.id));
    await refreshHistory();
    setSelected(null);
    setToast(data.snapshot ? "资产已移除，今日历史已更新" : "资产已移除，今日历史暂未更新");
  }

  async function updateAsset(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selected) return;
    setUpdating(true);
    const form = new FormData(event.currentTarget);
    try {
      const quantityAsset = isQuantityAsset(selected);
      const quantity = quantityAsset ? Number(form.get("quantity")) : undefined;
      let currentPrice = selectedMarket?.market_return?.currentPrice;
      let currency = selected.currency;
      if (quantityAsset && selected.code) {
        const marketResponse = await fetch(`/api/market?code=${encodeURIComponent(selected.code)}&category=${selected.category}&days=${lookback * 365}`);
        const market = await marketResponse.json();
        if (!marketResponse.ok || !market.currentPrice || !market.priceCurrency) throw new Error(market.error || "暂时无法读取实时价格");
        currentPrice = market.currentPrice;
        currency = market.priceCurrency;
        setMarketRates((current) => ({ ...current, [marketKey(selected.category, selected.code!)]: market }));
      }
      const investment = supportsInvestment(selected) ? {
        investmentStrategy: form.get("investmentStrategy") || "none",
        investmentAmount: Number(form.get("investmentAmount")) || undefined,
      } : {};
      const response = await fetch("/api/assets", {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          id: selected.id,
          amount: quantityAsset ? quantity! * currentPrice! : Number(form.get("amount")),
          quantity,
          currency: quantityAsset ? currency : form.get("currency"),
          ...investment,
        }),
      });
      const data = await response.json();
      if (response.status === 401) {
        setUser(null);
        return;
      }
      if (!response.ok) return setToast(data.error || "修改失败，请重试");
      const updated = {
        ...selected,
        amount: data.amount,
        quantity: data.quantity !== undefined && data.quantity !== null ? data.quantity : selected.quantity,
        currency: data.currency as Currency,
        investment_strategy: data.investmentStrategy !== undefined ? data.investmentStrategy : selected.investment_strategy,
        investment_amount: data.investmentAmount !== undefined ? data.investmentAmount : selected.investment_amount,
      };
      setAssets((current) => current.map((asset) => asset.id === updated.id ? updated : asset));
      setSelected(updated);
      await refreshHistory();
      setToast(data.snapshot ? `${quantityAsset ? "持有数量" : "资产金额与币种"}已保存，今日历史已更新` : "资产已保存，今日历史暂未更新");
    } catch (error) {
      setToast(error instanceof Error ? error.message : "修改失败，请重试");
    } finally {
      setUpdating(false);
    }
  }

  async function logout() {
    await fetch("/api/auth/logout", { method: "POST" });
    setUser(null);
    setAssets([]);
    setMarketRates({});
    setHistory([]);
    setAssetsLoading(false);
    setSelected(null);
    setExchangeRates({ CNY: 1 });
    setExchangeDate("");
  }

  if (authLoading) return <LoadingScreen label="正在打开本地账本…" />;
  if (!user) return <AuthScreen onAuthenticated={(authenticatedUser) => {
    setAssetsLoading(true);
    setUser(authenticatedUser);
  }} />;
  if (assetsLoading) return <LoadingScreen label="正在读取你的资产…" />;

  return (
    <main className="app-shell">
      <header className="topbar">
        <a className="brand" href="#top" aria-label="复利簿首页">
          <span className="brand-mark">复</span><span>复利簿</span>
        </a>
        <nav aria-label="主要导航">
          <a className="nav-active" href="#overview">总览</a>
          <a href="#assets">资产</a>
          <a href="#forecast">预测</a>
          <a href="#history">历史</a>
        </nav>
        <div className="header-actions">
          <div className="user-chip"><span className="avatar">{user.displayName.slice(0, 1)}</span><span className="user-meta"><strong>{user.displayName}</strong><small>{user.email}</small></span></div>
          <button className="logout-button" onClick={logout}>退出</button>
        </div>
      </header>

      <section className="content" id="top">
        <div className="welcome-row">
          <div>
            <p className="eyebrow">{new Date().toLocaleDateString("zh-CN", { year: "numeric", month: "long", day: "numeric" })} · 资产总览</p>
            <h1>{user.displayName}，看看财富生长到哪里了。</h1>
          </div>
          <button className="primary-button" onClick={() => setModalOpen(true)}><span>＋</span> 记录资产</button>
        </div>

        <section className="summary-grid" id="overview">
          <article className="total-card">
            <div className="card-label"><span>总资产 · 折合人民币</span><button className={`exchange-status${exchangeStale ? " stale" : ""}`} onClick={refreshExchangeRates} disabled={exchangeLoading}>{exchangeLoading ? "正在更新汇率…" : exchangeDate ? `${exchangeDate} 汇率 · 刷新` : "刷新最新汇率"}</button></div>
            <div className="total-value">{missingExchangeRate ? "行情或汇率暂不可用" : money(total)}</div>
            <div className="change-row"><span className="change-pill">汇率折算</span><span>本月预估增长 {missingExchangeRate ? "等待汇率" : money(expectedGain / Math.max(1, horizon * 12))}</span></div>
            <div className="mini-stats">
              <div><span>可产生收益</span><strong>{missingExchangeRate ? "等待汇率" : money(total - (grouped.find((g) => g.category === "fixed")?.amount || 0))}</strong></div>
              <div><span>组合预期年化（根据最近{lookback}年数据计算）</span><strong>{missingExchangeRate ? "等待汇率" : `${weightedRate.toFixed(2)}%`}</strong><small className="portfolio-rate-note">{limitedHistoryCount ? `其中 ${limitedHistoryCount} 项历史不足` : "\u00a0"}</small></div>
            </div>
          </article>

          <article className="allocation-card">
            <div className="card-heading"><div><span className="card-kicker">资产配置</span><h2>钱放在了哪里</h2></div><button className="text-button" onClick={() => setActiveFilter("all")}>查看全部</button></div>
            <div className="allocation-body">
              {!missingExchangeRate ? <div className="donut" style={{ background: total ? `conic-gradient(${grouped.map((item, index) => {
                const before = grouped.slice(0, index).reduce((sum, group) => sum + group.amount, 0) / total * 100;
                const after = before + item.amount / total * 100;
                return `${categoryMeta[item.category].color} ${before}% ${after}%`;
              }).join(",")})` : "#edf1ee" }}><div><strong>{grouped.length}</strong><span>类资产</span></div></div> : <div className="unavailable-state">等待完整汇率后显示配置</div>}
              {!missingExchangeRate && <div className="allocation-list">
                {grouped.slice(0, 5).map((item) => <button key={item.category} onClick={() => setActiveFilter(item.category)}>
                  <span className="legend-dot" style={{ background: categoryMeta[item.category].color }} />
                  <span>{categoryMeta[item.category].name}</span><strong>{(item.amount / total * 100).toFixed(1)}%</strong>
                </button>)}
              </div>}
            </div>
          </article>
        </section>

        <section className="forecast-card" id="forecast">
          <div className="forecast-copy">
            <span className="card-kicker">未来收益推演</span>
            <h2>{horizon} 年后，预计拥有</h2>
            <div className="forecast-number">{missingExchangeRate ? "等待汇率" : money(forecast)}</div>
            <p>按当前组合与复利计算，预计新增 <b>{missingExchangeRate ? "等待汇率" : money(expectedGain)}</b></p>
            <div className="control-block">
              <span>预测到未来</span>
              <div className="segmented">{[1, 3, 5, 10].map((year) => <button className={horizon === year ? "active" : ""} key={year} onClick={() => setHorizon(year)}>{year}年</button>)}</div>
            </div>
            <div className="sync-row">
              <label>历史区间<select value={lookback} onChange={(event) => setLookback(Number(event.target.value))}><option value="1">近1年</option><option value="3">近3年</option><option value="5">近5年</option><option value="10">近10年</option></select></label>
              <button onClick={syncMarketRates} disabled={syncing}>{syncing ? "读取中…" : "刷新价格与收益率"}</button>
            </div>
          </div>
          <div className="chart-wrap" aria-label={`未来 ${horizon} 年资产预测折线图`}>
            <div className="chart-top"><span>资产增长曲线</span><span className="forecast-legend"><i /> 历史收益率外推</span></div>
            {!missingExchangeRate ? <div className="chart">
              <span className="y-label top">{money(maxChart)}</span><span className="y-label bottom">{money(minChart)}</span>
              <div className="gridline gridline-1"/><div className="gridline gridline-2"/><div className="gridline gridline-3"/>
              <div className="bars">
                {chartValues.map((value, index) => {
                  const height = maxChart === minChart ? 12 : 18 + (value - minChart) / (maxChart - minChart) * 62;
                  return <div className="bar-column" key={index}><span className="bar-value">{index === chartValues.length - 1 ? `+${money(value - total)}` : ""}</span><div className="bar" style={{ height: `${height}%` }} /><small>{index === 0 ? "现在" : `${index}年`}</small></div>;
                })}
              </div>
            </div> : <div className="unavailable-chart">等待完整汇率后显示预测曲线</div>}
            <p className="disclaimer">预测基于历史收益率与输入利率，外币按当前汇率不变测算，不代表实际收益或投资承诺。</p>
          </div>
        </section>

        <HistorySection history={history} />

        <section className="assets-section" id="assets">
          <div className="section-heading"><div><span className="card-kicker">我的资产</span><h2>每一笔，都心中有数</h2></div><div className="section-actions"><span>{assets.length} 项资产</span><button className="primary-button compact" onClick={() => setModalOpen(true)}><span>＋</span> 记录资产</button></div></div>
          <div className="filter-row">
            <button className={activeFilter === "all" ? "active" : ""} onClick={() => setActiveFilter("all")}>全部</button>
            {(Object.keys(categoryMeta) as Category[]).map((category) => <button className={activeFilter === category ? "active" : ""} key={category} onClick={() => setActiveFilter(category)}>{categoryMeta[category].name}</button>)}
          </div>
          <div className="asset-list">
            {filtered.length === 0 && <div className="empty-assets"><span>＋</span><h3>{activeFilter === "all" ? "账本还是空的" : `还没有${categoryMeta[activeFilter as Category].name}资产`}</h3><p>从记录第一项资产开始，慢慢建立属于你的全资产视图。</p><button className="primary-button" onClick={() => setModalOpen(true)}>记录第一项资产</button></div>}
            {filtered.map((asset) => {
              const meta = categoryMeta[asset.category];
              const cnyAmount = toCny(asset, exchangeRates);
              return <button className="asset-row" key={asset.id} onClick={() => setSelected(asset)}>
                <span className="asset-icon" style={{ background: `${meta.color}18`, color: meta.color }}>{meta.short}</span>
                <span className="asset-main"><strong>{asset.name}</strong><small>{meta.name}{asset.code ? ` · ${asset.code}` : ""}{asset.quantity ? ` · ${quantityText(asset.quantity)} ${asset.category === "stock" ? "股" : "份"}` : ""}{asset.category === "fund" && asset.investment_strategy && asset.investment_strategy !== "none" ? ` · 定投${asset.investment_amount ? ` ${asset.investment_amount / 100}` : ""}` : ""} · {asset.note}</small></span>
                <span className="asset-rate">
                  <small>{asset.category === "fixed" ? "不计收益" : "预测年化"}</small>
                  <strong className={asset.annual_rate < 0 ? "negative" : ""}>{asset.category === "fixed" ? "—" : `${asset.annual_rate.toFixed(2)}%`}</strong>
                  <em className={asset.market_return?.historyLimited ? "asset-rate-note limited" : "asset-rate-note"}>{asset.market_return?.historyLimited ? <>历史不足，使用 {asset.market_return.actualDays} 天的数据计算</> : asset.market_return?.stale ? `旧数据 · ${asset.market_return.calculationDate}` : "\u00a0"}</em>
                </span>
                <span className="asset-amount"><strong>{originalMoney(asset.amount, asset.currency)}</strong><small>{asset.quantity && asset.market_return?.currentPrice ? `${quantityText(asset.quantity)} × ${priceText(asset.market_return.currentPrice, asset.currency)}` : asset.currency === "CNY" ? "人民币" : exchangeRates[asset.currency] ? `≈ ${money(cnyAmount)} · ${currencyMeta[asset.currency]}` : "等待汇率"}{!missingExchangeRate && total && cnyAmount ? ` · ${(cnyAmount / total * 100).toFixed(1)}%` : ""}</small></span>
                <span className="chevron">›</span>
              </button>;
            })}
          </div>
        </section>
      </section>

      <footer><span><b>复利簿</b> · 看清资产，也看见时间的力量</span><span>数据仅作个人资产记录与测算参考</span></footer>

      {modalOpen && <div className="modal-backdrop" onMouseDown={(event) => event.target === event.currentTarget && setModalOpen(false)}>
        <section className="modal" role="dialog" aria-modal="true" aria-labelledby="modal-title">
          <button className="modal-close" onClick={() => setModalOpen(false)} aria-label="关闭">×</button>
          <span className="card-kicker">新增记录</span><h2 id="modal-title">把一项资产放进账本</h2><p>股票按股数、基金按份数记录，市值会读取最新价格自动计算。</p>
          <AssetForm onSubmit={addAsset} saving={saving} />
        </section>
      </div>}

      {selectedMarket && <div className="modal-backdrop" onMouseDown={(event) => event.target === event.currentTarget && setSelected(null)}>
        <aside className="detail-panel" role="dialog" aria-modal="true">
          <button className="modal-close" onClick={() => setSelected(null)} aria-label="关闭">×</button>
          <span className="asset-icon large" style={{ background: `${categoryMeta[selectedMarket.category].color}18`, color: categoryMeta[selectedMarket.category].color }}>{categoryMeta[selectedMarket.category].short}</span>
          <span className="card-kicker">{categoryMeta[selectedMarket.category].name}</span><h2>{selectedMarket.name}</h2><p>{selectedMarket.note || "暂无备注"}</p>
          <form className="asset-edit-form" key={`${selectedMarket.id}-${selectedMarket.amount}-${selectedMarket.quantity}-${selectedMarket.currency}-${selectedMarket.investment_strategy}-${selectedMarket.investment_amount}`} onSubmit={updateAsset}>
            <div className="edit-heading"><strong>修改资产</strong><span>{isQuantityAsset(selectedMarket) ? "修改数量后按最新价格重新计算市值" : supportsInvestment(selectedMarket) ? "可修改市值、币种和定投设置" : "仅可修改币种和当前市值"}</span></div>
            {isQuantityAsset(selectedMarket)
              ? <label><span>持有{selectedMarket.category === "stock" ? "股数" : "份数"}</span><input required name="quantity" type="number" min="0.00000001" step="any" defaultValue={selectedMarket.quantity ?? ""} /></label>
              : <div className="form-two"><label><span>计价币种</span><select name="currency" defaultValue={selectedMarket.currency}>{(Object.keys(currencyMeta) as Currency[]).map((code) => <option value={code} key={code}>{currencyMeta[code]} · {code}</option>)}</select></label><label><span>当前市值</span><input required name="amount" type="number" min="0.01" step="0.01" defaultValue={(selectedMarket.amount / 100).toFixed(2)} /></label></div>}
            {supportsInvestment(selectedMarket) && <EditableInvestmentFields asset={selectedMarket} />}
            <button className="save-edit-button" disabled={updating}>{updating ? "正在保存…" : "保存修改"}</button>
          </form>
          <dl>{selectedMarket.quantity && <div><dt>持有数量</dt><dd>{quantityText(selectedMarket.quantity)} {selectedMarket.category === "stock" ? "股" : "份"}</dd></div>}{selectedMarket.market_return?.currentPrice && <div><dt>最新价格</dt><dd>{priceText(selectedMarket.market_return.currentPrice, selectedMarket.currency)} · {selectedMarket.market_return.priceDate}</dd></div>}{selectedMarket.currency !== "CNY" && <div><dt>折合人民币</dt><dd>{exchangeRates[selectedMarket.currency] ? money(toCny(selectedMarket, exchangeRates)) : "等待汇率"}</dd></div>}<div><dt>预测年化</dt><dd>{selectedMarket.category === "fixed" ? "不计收益" : `${selectedMarket.annual_rate.toFixed(2)}%`}</dd></div>{selectedMarket.code && <div><dt>资产代码</dt><dd>{selectedMarket.code}</dd></div>}{selectedMarket.market_return && <><div><dt>请求历史区间</dt><dd>{selectedMarket.market_return.requestedDays} 天</dd></div><div><dt>实际行情区间</dt><dd>{selectedMarket.market_return.startDate} 至 {selectedMarket.market_return.endDate} · {selectedMarket.market_return.actualDays} 天</dd></div><div><dt>行情缓存日期</dt><dd>{selectedMarket.market_return.calculationDate}{selectedMarket.market_return.stale ? " · 旧数据" : ""}</dd></div></>}<div><dt>{horizon} 年后预计</dt><dd>{originalMoney(calculatePortfolio([selectedMarket], { [selectedMarket.currency]: 1 }, horizon)?.forecast ?? selectedMarket.amount, selectedMarket.currency)}</dd></div></dl>
          <button className="danger-button" onClick={() => removeAsset(selectedMarket)}>删除这项资产</button>
        </aside>
      </div>}

      {toast && <div className="toast" role="status">{toast}</div>}
    </main>
  );
}

function EditableInvestmentFields({ asset }: { asset: Asset }) {
  const [strategy, setStrategy] = useState(asset.investment_strategy || "none");
  return <div className="form-two">
    <label><span>定投策略</span><select name="investmentStrategy" value={strategy} onChange={(event) => setStrategy(event.target.value as Asset["investment_strategy"] || "none")}><option value="none">不定投</option><option value="monthly">每月第一个交易日</option><option value="weekly">每周第一个交易日</option><option value="yearly">每年第一个交易日</option><option value="daily">每个交易日</option></select></label>
    <label><span>定投金额（{asset.currency}）</span><input required={strategy !== "none"} disabled={strategy === "none"} name="investmentAmount" type="number" min="0.01" step="0.01" defaultValue={asset.investment_amount ? (asset.investment_amount / 100).toFixed(2) : ""} /></label>
  </div>;
}

function HistorySection({ history }: { history: HistoryEntry[] }) {
  const rows = history.map((entry, index) => {
    const previous = history[index - 1];
    const change = previous ? entry.total_cny - previous.total_cny : null;
    const changeRate = previous && previous.total_cny ? (change! / previous.total_cny) * 100 : null;
    return { ...entry, change, changeRate };
  });
  const values = history.map((entry) => entry.total_cny);
  const min = values.length ? Math.min(...values) : 0;
  const max = values.length ? Math.max(...values) : 0;
  const chartWidth = 680;
  const chartHeight = 240;
  const paddingX = 34;
  const paddingY = 28;
  const points = history.map((entry, index) => {
    const x = paddingX + index / Math.max(1, history.length - 1) * (chartWidth - paddingX * 2);
    const y = paddingY + (max === min ? 0.5 : (max - entry.total_cny) / (max - min)) * (chartHeight - paddingY * 2);
    return { x, y };
  });
  const totalChange = history.length > 1 ? history.at(-1)!.total_cny - history[0].total_cny : 0;

  return <section className="history-section" id="history">
    <div className="section-heading history-heading">
      <div><span className="card-kicker">资产历史</span><h2>总资产走过的轨迹</h2></div>
      <span className="history-count">{history.length} 天记录</span>
    </div>
    {history.length === 0 ? <div className="history-empty"><strong>还没有历史记录</strong><span>新增、修改或删除资产后，这里会保存当天最后一次总资产。</span></div> : <>
      {history.length >= 2 ? <div className="history-chart-grid">
        <div className="history-chart" aria-label="历史总资产折线图">
          <div className="history-chart-labels"><span>{money(max)}</span><span>{money(min)}</span></div>
          <svg viewBox={`0 0 ${chartWidth} ${chartHeight}`} role="img" aria-label="按日期排列的历史总资产走势">
            <line x1={paddingX} y1={paddingY} x2={chartWidth - paddingX} y2={paddingY} />
            <line x1={paddingX} y1={chartHeight / 2} x2={chartWidth - paddingX} y2={chartHeight / 2} />
            <line x1={paddingX} y1={chartHeight - paddingY} x2={chartWidth - paddingX} y2={chartHeight - paddingY} />
            <polyline points={points.map((point) => `${point.x},${point.y}`).join(" ")} />
            {points.map((point, index) => <circle key={history[index].snapshot_date} cx={point.x} cy={point.y} r="4"><title>{history[index].snapshot_date} · {money(history[index].total_cny)}</title></circle>)}
          </svg>
          <div className="history-axis"><span>{history[0].snapshot_date}</span><span>{history.at(-1)!.snapshot_date}</span></div>
        </div>
        <div className="history-summary">
          <span>最新总资产</span><strong>{money(history.at(-1)!.total_cny)}</strong>
          <small>区间变化</small><b className={totalChange < 0 ? "negative" : "positive"}>{totalChange > 0 ? "+" : ""}{money(totalChange)}</b>
        </div>
      </div> : <div className="history-empty compact"><strong>已记录今天的总资产</strong><span>再产生一天记录后显示走势。</span></div>}
      <div className="history-table-wrap">
        <table className="history-table">
          <thead><tr><th>日期</th><th>总资产</th><th>较上次</th><th>变化率</th></tr></thead>
          <tbody>{[...rows].reverse().map((row) => <tr key={row.snapshot_date}>
            <td>{row.snapshot_date}</td><td>{money(row.total_cny)}</td>
            <td className={row.change === null ? "" : row.change < 0 ? "negative" : "positive"}>{row.change === null ? "—" : `${row.change > 0 ? "+" : ""}${money(row.change)}`}</td>
            <td className={row.changeRate === null ? "" : row.changeRate < 0 ? "negative" : "positive"}>{row.changeRate === null ? "—" : `${row.changeRate > 0 ? "+" : ""}${row.changeRate.toFixed(2)}%`}</td>
          </tr>)}</tbody>
        </table>
      </div>
    </>}
  </section>;
}

function AssetForm({ onSubmit, saving }: { onSubmit: (event: FormEvent<HTMLFormElement>) => void; saving: boolean }) {
  const [category, setCategory] = useState<Category>("stock");
  const [currency, setCurrency] = useState<Currency>("CNY");
  const [investmentStrategy, setInvestmentStrategy] = useState("none");
  const needsCode = ["stock", "fund", "money"].includes(category);
  const quantityAsset = category === "stock" || category === "fund";
  const needsRate = ["deposit", "housing"].includes(category);
  return <form className="asset-form" onSubmit={onSubmit}>
    <label><span>资产类型</span><select name="category" value={category} onChange={(event) => setCategory(event.target.value as Category)}>{(Object.keys(categoryMeta) as Category[]).map((key) => <option value={key} key={key}>{categoryMeta[key].name}</option>)}</select></label>
    <label><span>资产名称</span><input required name="name" placeholder={category === "fixed" ? "例如：自住房产" : "例如：沪深300指数基金"} /></label>
    {needsCode && <label><span>{category === "stock" ? "股票代码" : "基金代码"}</span><input required name="code" inputMode="text" autoCapitalize="characters" placeholder="例如 510300、QQQ、VOO" maxLength={16} onChange={(event) => {
      const code = event.target.value.trim();
      if (/^[a-z][a-z0-9.-]*$/i.test(code)) setCurrency("USD");
      else if (/^\d{6}$/.test(code)) setCurrency("CNY");
    }} /><small>支持国内 6 位代码和美股代码；QQQ 等美股代码会自动选择美元，也可手动修改</small></label>}
    {quantityAsset
      ? <label><span>持有{category === "stock" ? "股数" : "份数"}</span><input required name="quantity" type="number" min="0.00000001" step="any" placeholder={category === "stock" ? "例如 100.5" : "例如 1250.75"} /><small>支持小数；保存时会读取最新价格并自动计算当前市值和计价币种</small></label>
      : <div className="form-two"><label><span>计价币种</span><select name="currency" value={currency} onChange={(event) => setCurrency(event.target.value as Currency)}>{(Object.keys(currencyMeta) as Currency[]).map((code) => <option value={code} key={code}>{currencyMeta[code]} · {code}</option>)}</select></label><label><span>当前市值（{currency}）</span><input required name="amount" type="number" min="0.01" step="0.01" placeholder={currency === "CNY" ? "100000" : "10000"} /></label></div>}
    {needsRate && <label><span>年利率（%）</span><input required name="annualRate" type="number" step="0.01" min="0" placeholder="2.60" /></label>}
    {category === "fund" && <div className="form-two"><label><span>定投策略</span><select name="investmentStrategy" value={investmentStrategy} onChange={(event) => setInvestmentStrategy(event.target.value)}><option value="none">不定投</option><option value="monthly">每月第一个交易日</option><option value="weekly">每周第一个交易日</option><option value="yearly">每年第一个交易日</option><option value="daily">每个交易日</option></select></label><label><span>定投金额（{currency}）</span><input required={investmentStrategy !== "none"} name="investmentAmount" type="number" min="0.01" step="0.01" placeholder="1000" /></label></div>}
    <label><span>备注</span><input name="note" placeholder="可选，例如到期日或用途" /></label>
    <button className="primary-button submit" disabled={saving}>{saving ? "正在保存…" : "确认记录"}</button>
  </form>;
}

function LoadingScreen({ label }: { label: string }) {
  return <main className="loading-screen"><div className="loading-mark">复</div><div className="loading-line"><span /></div><p>{label}</p></main>;
}

function AuthScreen({ onAuthenticated }: { onAuthenticated: (user: User) => void }) {
  const [mode, setMode] = useState<"login" | "register">("login");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSubmitting(true);
    setError("");
    const form = new FormData(event.currentTarget);
    try {
      const response = await fetch(`/api/auth/${mode}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          displayName: form.get("displayName"),
          email: form.get("email"),
          password: form.get("password"),
        }),
      });
      const data = await response.json();
      if (!response.ok) {
        setError(data.error || (mode === "login" ? "登录失败" : "注册失败"));
        return;
      }
      onAuthenticated(data.user);
    } catch {
      setError("本地服务连接失败，请确认项目仍在运行");
    } finally {
      setSubmitting(false);
    }
  }

  function changeMode(next: "login" | "register") {
    setMode(next);
    setError("");
  }

  return <main className="auth-page">
    <section className="auth-story">
      <a className="brand auth-brand" href="#" aria-label="复利簿"><span className="brand-mark">复</span><span>复利簿</span></a>
      <div className="auth-copy"><span className="auth-overline">你的本地资产账本</span><h1>让每一笔资产<br/>都有自己的位置。</h1><p>记录、整理、推演。所有账户与资产数据只保存在这台设备的本地 SQLite 数据库中。</p></div>
      <div className="auth-preview" aria-hidden="true">
        <div className="auth-preview-head"><span>资产组合</span><span>本地保存</span></div>
        <strong>¥ 1,326,800</strong>
        <div className="auth-growth"><i style={{ height: "28%" }}/><i style={{ height: "39%" }}/><i style={{ height: "48%" }}/><i style={{ height: "63%" }}/><i style={{ height: "82%" }}/></div>
        <div className="auth-preview-foot"><span>股票 · 基金 · 存款</span><b>预计稳步增长</b></div>
      </div>
      <p className="auth-local-note"><span>✓</span> 无 MySQL　<span>✓</span> 无云端用户库　<span>✓</span> 密码加盐哈希</p>
    </section>

    <section className="auth-panel">
      <div className="auth-card">
        <span className="card-kicker">欢迎使用复利簿</span>
        <h2>{mode === "login" ? "登录你的本地账本" : "创建一个本地账户"}</h2>
        <p>{mode === "login" ? "使用本机注册的邮箱与密码继续。" : "账户只在这台设备上有效，不会发送到云端。"}</p>
        <div className="auth-tabs"><button className={mode === "login" ? "active" : ""} onClick={() => changeMode("login")}>登录</button><button className={mode === "register" ? "active" : ""} onClick={() => changeMode("register")}>注册</button></div>
        <form className="auth-form" onSubmit={submit}>
          {mode === "register" && <label><span>昵称</span><input name="displayName" required minLength={2} maxLength={40} autoComplete="nickname" placeholder="怎么称呼你" /></label>}
          <label><span>邮箱</span><input name="email" required type="email" autoComplete="email" placeholder="name@example.com" /></label>
          <label><span>密码</span><input name="password" required type="password" minLength={8} maxLength={128} autoComplete={mode === "login" ? "current-password" : "new-password"} placeholder={mode === "login" ? "输入密码" : "至少 8 个字符"} /></label>
          {error && <div className="auth-error" role="alert">{error}</div>}
          <button className="primary-button auth-submit" disabled={submitting}>{submitting ? "请稍候…" : mode === "login" ? "登录" : "创建账户"}</button>
        </form>
        <p className="auth-switch">{mode === "login" ? "还没有本地账户？" : "已经有账户？"}<button onClick={() => changeMode(mode === "login" ? "register" : "login")}>{mode === "login" ? "立即注册" : "返回登录"}</button></p>
      </div>
    </section>
  </main>;
}

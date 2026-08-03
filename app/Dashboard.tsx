"use client";

import { FormEvent, useEffect, useMemo, useState } from "react";

type Category = "stock" | "fund" | "money" | "deposit" | "housing" | "fixed";
type Currency = "CNY" | "USD" | "HKD" | "EUR" | "JPY" | "GBP" | "SGD" | "AUD" | "CAD" | "CHF";
type Asset = {
  id: number;
  name: string;
  category: Category;
  code: string | null;
  amount: number;
  currency: Currency;
  annual_rate: number;
  note: string;
  created_at: string;
};
type User = { id: number; email: string; displayName: string };

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

export default function Dashboard() {
  const [user, setUser] = useState<User | null>(null);
  const [authLoading, setAuthLoading] = useState(true);
  const [assetsLoading, setAssetsLoading] = useState(false);
  const [assets, setAssets] = useState<Asset[]>([]);
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
    ])
      .then(([assetData, rateData]) => {
        setAssets(assetData.assets ?? []);
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
    if (!toast) return;
    const timer = window.setTimeout(() => setToast(""), 2800);
    return () => window.clearTimeout(timer);
  }, [toast]);

  const missingExchangeRate = assets.some((asset) => !exchangeRates[asset.currency]);
  const total = useMemo(() => assets.reduce((sum, asset) => sum + toCny(asset, exchangeRates), 0), [assets, exchangeRates]);
  const forecast = useMemo(
    () => assets.reduce((sum, asset) => {
      const rate = asset.category === "fixed" ? 0 : asset.annual_rate / 100;
      return sum + toCny(asset, exchangeRates) * Math.pow(1 + rate, horizon);
    }, 0),
    [assets, exchangeRates, horizon]
  );
  const expectedGain = forecast - total;
  const weightedRate = total
    ? assets.reduce((sum, asset) => sum + toCny(asset, exchangeRates) * asset.annual_rate, 0) / total
    : 0;

  const grouped = useMemo(() => {
    return (Object.keys(categoryMeta) as Category[]).map((category) => ({
      category,
      amount: assets.filter((item) => item.category === category).reduce((sum, item) => sum + toCny(item, exchangeRates), 0),
    })).filter((item) => item.amount > 0);
  }, [assets, exchangeRates]);

  const filtered = activeFilter === "all" ? assets : assets.filter((asset) => asset.category === activeFilter);
  const chartValues = Array.from({ length: horizon + 1 }, (_, index) => {
    return assets.reduce((sum, asset) => {
      const rate = asset.category === "fixed" ? 0 : asset.annual_rate / 100;
      return sum + toCny(asset, exchangeRates) * Math.pow(1 + rate, index);
    }, 0);
  });
  const minChart = Math.min(...chartValues);
  const maxChart = Math.max(...chartValues);

  async function addAsset(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSaving(true);
    const form = new FormData(event.currentTarget);
    const category = form.get("category") as Category;
    let rate = Number(form.get("annualRate")) || 0;
    const code = String(form.get("code") || "").trim().toUpperCase();
    if (code && ["stock", "fund", "money"].includes(category)) {
      try {
        const marketResponse = await fetch(`/api/market?code=${encodeURIComponent(code)}&category=${category}&days=${lookback * 365}`);
        const market = await marketResponse.json();
        if (marketResponse.ok) rate = Number(market.annualRate.toFixed(2));
      } catch { /* retain manual/default rate */ }
    }
    const response = await fetch("/api/assets", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        name: form.get("name"), category, code,
        amount: Number(form.get("amount")), currency: form.get("currency"), annualRate: rate, note: form.get("note"),
      }),
    });
    const data = await response.json();
    setSaving(false);
    if (response.status === 401) {
      setUser(null);
      return;
    }
    if (!response.ok) return setToast(data.error || "保存失败，请重试");
    setAssets((current) => [...current, data.asset]);
    setModalOpen(false);
    setToast("资产已加入总览");
  }

  async function syncMarketRates() {
    setSyncing(true);
    const results = await Promise.all(assets.map(async (asset) => {
      if (!asset.code || !["stock", "fund", "money"].includes(asset.category)) {
        return { asset, updated: false, eligible: false };
      }
      try {
        const response = await fetch(`/api/market?code=${encodeURIComponent(asset.code)}&category=${asset.category}&days=${lookback * 365}`);
        const data = await response.json();
        if (!response.ok) return { asset, updated: false, eligible: true };
        const annualRate = Number(data.annualRate.toFixed(2));
        const saved = await fetch("/api/assets", {
          method: "PATCH",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ id: asset.id, annualRate }),
        });
        return saved.ok
          ? { asset: { ...asset, annual_rate: annualRate }, updated: true, eligible: true }
          : { asset, updated: false, eligible: true };
      } catch { return { asset, updated: false, eligible: true }; }
    }));
    setAssets(results.map((result) => result.asset));
    setSyncing(false);
    const eligible = results.filter((result) => result.eligible).length;
    const updated = results.filter((result) => result.updated).length;
    setToast(eligible === 0 ? "当前没有可同步行情的股票或基金" : updated === eligible
      ? `已更新并保存 ${updated} 项收益率`
      : `已更新 ${updated}/${eligible} 项，其余行情暂不可用`);
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
      setToast(data.stale ? "实时汇率暂不可用，已继续使用上次汇率" : "最新汇率已更新");
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
    if (!response.ok) return setToast("删除失败，请重试");
    setAssets((current) => current.filter((item) => item.id !== asset.id));
    setSelected(null);
    setToast("资产已移除");
  }

  async function updateAsset(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selected) return;
    setUpdating(true);
    const form = new FormData(event.currentTarget);
    try {
      const response = await fetch("/api/assets", {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          id: selected.id,
          amount: Number(form.get("amount")),
          currency: form.get("currency"),
        }),
      });
      const data = await response.json();
      if (response.status === 401) {
        setUser(null);
        return;
      }
      if (!response.ok) return setToast(data.error || "修改失败，请重试");
      const updated = { ...selected, amount: data.amount, currency: data.currency as Currency };
      setAssets((current) => current.map((asset) => asset.id === updated.id ? updated : asset));
      setSelected(updated);
      setToast("资产金额与币种已保存");
    } catch {
      setToast("修改失败，请重试");
    } finally {
      setUpdating(false);
    }
  }

  async function logout() {
    await fetch("/api/auth/logout", { method: "POST" });
    setUser(null);
    setAssets([]);
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
            <div className="total-value">{missingExchangeRate ? "汇率暂不可用" : money(total)}</div>
            <div className="change-row"><span className="change-pill">汇率折算</span><span>本月预估增长 {missingExchangeRate ? "等待汇率" : money(expectedGain / Math.max(1, horizon * 12))}</span></div>
            <div className="mini-stats">
              <div><span>可产生收益</span><strong>{money(total - (grouped.find((g) => g.category === "fixed")?.amount || 0))}</strong></div>
              <div><span>组合预期年化</span><strong>{weightedRate.toFixed(2)}%</strong></div>
            </div>
          </article>

          <article className="allocation-card">
            <div className="card-heading"><div><span className="card-kicker">资产配置</span><h2>钱放在了哪里</h2></div><button className="text-button" onClick={() => setActiveFilter("all")}>查看全部</button></div>
            <div className="allocation-body">
              <div className="donut" style={{ background: total ? `conic-gradient(${grouped.map((item, index) => {
                const before = grouped.slice(0, index).reduce((sum, group) => sum + group.amount, 0) / total * 100;
                const after = before + item.amount / total * 100;
                return `${categoryMeta[item.category].color} ${before}% ${after}%`;
              }).join(",")})` : "#edf1ee" }}><div><strong>{grouped.length}</strong><span>类资产</span></div></div>
              <div className="allocation-list">
                {grouped.slice(0, 5).map((item) => <button key={item.category} onClick={() => setActiveFilter(item.category)}>
                  <span className="legend-dot" style={{ background: categoryMeta[item.category].color }} />
                  <span>{categoryMeta[item.category].name}</span><strong>{(item.amount / total * 100).toFixed(1)}%</strong>
                </button>)}
              </div>
            </div>
          </article>
        </section>

        <section className="forecast-card" id="forecast">
          <div className="forecast-copy">
            <span className="card-kicker">未来收益推演</span>
            <h2>{horizon} 年后，预计拥有</h2>
            <div className="forecast-number">{missingExchangeRate ? "等待汇率" : money(forecast)}</div>
            <p>按当前组合与复利计算，预计新增 <b>{money(expectedGain)}</b></p>
            <div className="control-block">
              <span>预测到未来</span>
              <div className="segmented">{[1, 3, 5, 10].map((year) => <button className={horizon === year ? "active" : ""} key={year} onClick={() => setHorizon(year)}>{year}年</button>)}</div>
            </div>
            <div className="sync-row">
              <label>历史区间<select value={lookback} onChange={(event) => setLookback(Number(event.target.value))}><option value="1">近1年</option><option value="3">近3年</option><option value="5">近5年</option></select></label>
              <button onClick={syncMarketRates} disabled={syncing}>{syncing ? "读取中…" : "读取最新收益率"}</button>
            </div>
          </div>
          <div className="chart-wrap" aria-label={`未来 ${horizon} 年资产预测折线图`}>
            <div className="chart-top"><span>资产增长曲线</span><span className="forecast-legend"><i /> 历史收益率外推</span></div>
            <div className="chart">
              <span className="y-label top">{money(maxChart)}</span><span className="y-label bottom">{money(minChart)}</span>
              <div className="gridline gridline-1"/><div className="gridline gridline-2"/><div className="gridline gridline-3"/>
              <div className="bars">
                {chartValues.map((value, index) => {
                  const height = maxChart === minChart ? 12 : 18 + (value - minChart) / (maxChart - minChart) * 62;
                  return <div className="bar-column" key={index}><span className="bar-value">{index === chartValues.length - 1 ? `+${money(value - total)}` : ""}</span><div className="bar" style={{ height: `${height}%` }} /><small>{index === 0 ? "现在" : `${index}年`}</small></div>;
                })}
              </div>
            </div>
            <p className="disclaimer">预测基于历史收益率与输入利率，外币按当前汇率不变测算，不代表实际收益或投资承诺。</p>
          </div>
        </section>

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
                <span className="asset-main"><strong>{asset.name}</strong><small>{meta.name}{asset.code ? ` · ${asset.code}` : ""} · {asset.note}</small></span>
                <span className="asset-rate"><small>{asset.category === "fixed" ? "不计收益" : "预测年化"}</small><strong className={asset.annual_rate < 0 ? "negative" : ""}>{asset.category === "fixed" ? "—" : `${asset.annual_rate.toFixed(2)}%`}</strong></span>
                <span className="asset-amount"><strong>{originalMoney(asset.amount, asset.currency)}</strong><small>{asset.currency === "CNY" ? "人民币" : cnyAmount ? `≈ ${money(cnyAmount)} · ${currencyMeta[asset.currency]}` : "等待汇率"}{total && cnyAmount ? ` · ${(cnyAmount / total * 100).toFixed(1)}%` : ""}</small></span>
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
          <span className="card-kicker">新增记录</span><h2 id="modal-title">把一项资产放进账本</h2><p>按资产实际币种录入市值；总览会按最新汇率折合人民币。</p>
          <AssetForm onSubmit={addAsset} saving={saving} />
        </section>
      </div>}

      {selected && <div className="modal-backdrop" onMouseDown={(event) => event.target === event.currentTarget && setSelected(null)}>
        <aside className="detail-panel" role="dialog" aria-modal="true">
          <button className="modal-close" onClick={() => setSelected(null)} aria-label="关闭">×</button>
          <span className="asset-icon large" style={{ background: `${categoryMeta[selected.category].color}18`, color: categoryMeta[selected.category].color }}>{categoryMeta[selected.category].short}</span>
          <span className="card-kicker">{categoryMeta[selected.category].name}</span><h2>{selected.name}</h2><p>{selected.note || "暂无备注"}</p>
          <form className="asset-edit-form" key={`${selected.id}-${selected.amount}-${selected.currency}`} onSubmit={updateAsset}>
            <div className="edit-heading"><strong>修改资产</strong><span>仅可修改币种和当前市值</span></div>
            <div className="form-two"><label><span>计价币种</span><select name="currency" defaultValue={selected.currency}>{(Object.keys(currencyMeta) as Currency[]).map((code) => <option value={code} key={code}>{currencyMeta[code]} · {code}</option>)}</select></label><label><span>当前市值</span><input required name="amount" type="number" min="0.01" step="0.01" defaultValue={(selected.amount / 100).toFixed(2)} /></label></div>
            <button className="save-edit-button" disabled={updating}>{updating ? "正在保存…" : "保存修改"}</button>
          </form>
          <dl>{selected.currency !== "CNY" && <div><dt>折合人民币</dt><dd>{exchangeRates[selected.currency] ? money(toCny(selected, exchangeRates)) : "等待汇率"}</dd></div>}<div><dt>预测年化</dt><dd>{selected.category === "fixed" ? "不计收益" : `${selected.annual_rate.toFixed(2)}%`}</dd></div>{selected.code && <div><dt>资产代码</dt><dd>{selected.code}</dd></div>}<div><dt>{horizon} 年后预计</dt><dd>{originalMoney(selected.amount * Math.pow(1 + (selected.category === "fixed" ? 0 : selected.annual_rate / 100), horizon), selected.currency)}</dd></div></dl>
          <button className="danger-button" onClick={() => removeAsset(selected)}>删除这项资产</button>
        </aside>
      </div>}

      {toast && <div className="toast" role="status">{toast}</div>}
    </main>
  );
}

function AssetForm({ onSubmit, saving }: { onSubmit: (event: FormEvent<HTMLFormElement>) => void; saving: boolean }) {
  const [category, setCategory] = useState<Category>("stock");
  const [currency, setCurrency] = useState<Currency>("CNY");
  const needsCode = ["stock", "fund", "money"].includes(category);
  const needsRate = ["deposit", "housing"].includes(category);
  return <form className="asset-form" onSubmit={onSubmit}>
    <label><span>资产类型</span><select name="category" value={category} onChange={(event) => setCategory(event.target.value as Category)}>{(Object.keys(categoryMeta) as Category[]).map((key) => <option value={key} key={key}>{categoryMeta[key].name}</option>)}</select></label>
    <label><span>资产名称</span><input required name="name" placeholder={category === "fixed" ? "例如：自住房产" : "例如：沪深300指数基金"} /></label>
    {needsCode && <label><span>股票 / 基金代码</span><input required name="code" inputMode="text" autoCapitalize="characters" placeholder="例如 510300、QQQ、VOO" maxLength={16} onChange={(event) => {
      const code = event.target.value.trim();
      if (/^[a-z][a-z0-9.-]*$/i.test(code)) setCurrency("USD");
      else if (/^\d{6}$/.test(code)) setCurrency("CNY");
    }} /><small>支持国内 6 位代码和美股代码；QQQ 等美股代码会自动选择美元，也可手动修改</small></label>}
    <div className="form-two"><label><span>计价币种</span><select name="currency" value={currency} onChange={(event) => setCurrency(event.target.value as Currency)}>{(Object.keys(currencyMeta) as Currency[]).map((code) => <option value={code} key={code}>{currencyMeta[code]} · {code}</option>)}</select></label><label><span>当前市值（{currency}）</span><input required name="amount" type="number" min="0.01" step="0.01" placeholder={currency === "CNY" ? "100000" : "10000"} /></label></div>
    {needsRate && <label><span>年利率（%）</span><input required name="annualRate" type="number" step="0.01" min="0" placeholder="2.60" /></label>}
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

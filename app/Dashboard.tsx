"use client";

import { FormEvent, useEffect, useMemo, useState } from "react";

type Category = "stock" | "fund" | "money" | "deposit" | "housing" | "fixed";
type Asset = {
  id: number;
  name: string;
  category: Category;
  code: string | null;
  amount: number;
  annual_rate: number;
  note: string;
  created_at: string;
};

const categoryMeta: Record<Category, { name: string; short: string; color: string }> = {
  stock: { name: "股票", short: "股", color: "#ee6a4d" },
  fund: { name: "基金", short: "基", color: "#a78bfa" },
  money: { name: "货币基金", short: "货", color: "#28a88a" },
  deposit: { name: "存款", short: "存", color: "#e7b344" },
  housing: { name: "公积金", short: "积", color: "#5196e3" },
  fixed: { name: "固定资产", short: "固", color: "#8c98a4" },
};

const fallbackAssets: Asset[] = [
  { id: 1, name: "贵州茅台", category: "stock", code: "600519", amount: 28640000, annual_rate: 8.6, note: "核心持仓", created_at: "" },
  { id: 2, name: "沪深300ETF", category: "fund", code: "510300", amount: 19860000, annual_rate: 6.8, note: "宽基配置", created_at: "" },
  { id: 3, name: "稳健货币基金", category: "money", code: "000198", amount: 12800000, annual_rate: 1.52, note: "流动资金", created_at: "" },
  { id: 4, name: "三年期定期存款", category: "deposit", code: null, amount: 30000000, annual_rate: 2.6, note: "2028 年到期", created_at: "" },
  { id: 5, name: "住房公积金", category: "housing", code: null, amount: 16000000, annual_rate: 1.5, note: "每月持续缴存", created_at: "" },
  { id: 6, name: "自住房产", category: "fixed", code: null, amount: 40800000, annual_rate: 0, note: "按保守估值记录", created_at: "" },
];

const money = (cents: number, digits = 0) =>
  new Intl.NumberFormat("zh-CN", {
    style: "currency",
    currency: "CNY",
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  }).format(cents / 100);

export default function Dashboard() {
  const [assets, setAssets] = useState<Asset[]>(fallbackAssets);
  const [activeFilter, setActiveFilter] = useState<"all" | Category>("all");
  const [horizon, setHorizon] = useState(3);
  const [lookback, setLookback] = useState(3);
  const [modalOpen, setModalOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [toast, setToast] = useState("");
  const [selected, setSelected] = useState<Asset | null>(null);

  useEffect(() => {
    fetch("/api/assets")
      .then((response) => response.json())
      .then((data) => data.assets?.length && setAssets(data.assets))
      .catch(() => undefined);
  }, []);

  useEffect(() => {
    if (!toast) return;
    const timer = window.setTimeout(() => setToast(""), 2800);
    return () => window.clearTimeout(timer);
  }, [toast]);

  const total = useMemo(() => assets.reduce((sum, asset) => sum + asset.amount, 0), [assets]);
  const forecast = useMemo(
    () => assets.reduce((sum, asset) => {
      const rate = asset.category === "fixed" ? 0 : asset.annual_rate / 100;
      return sum + asset.amount * Math.pow(1 + rate, horizon);
    }, 0),
    [assets, horizon]
  );
  const expectedGain = forecast - total;
  const weightedRate = total
    ? assets.reduce((sum, asset) => sum + asset.amount * asset.annual_rate, 0) / total
    : 0;

  const grouped = useMemo(() => {
    return (Object.keys(categoryMeta) as Category[]).map((category) => ({
      category,
      amount: assets.filter((item) => item.category === category).reduce((sum, item) => sum + item.amount, 0),
    })).filter((item) => item.amount > 0);
  }, [assets]);

  const filtered = activeFilter === "all" ? assets : assets.filter((asset) => asset.category === activeFilter);
  const chartValues = Array.from({ length: horizon + 1 }, (_, index) => {
    return assets.reduce((sum, asset) => sum + asset.amount * Math.pow(1 + asset.annual_rate / 100, index), 0);
  });
  const minChart = Math.min(...chartValues);
  const maxChart = Math.max(...chartValues);

  async function addAsset(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSaving(true);
    const form = new FormData(event.currentTarget);
    const category = form.get("category") as Category;
    let rate = Number(form.get("annualRate")) || 0;
    const code = String(form.get("code") || "").trim();
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
        amount: Number(form.get("amount")), annualRate: rate, note: form.get("note"),
      }),
    });
    const data = await response.json();
    setSaving(false);
    if (!response.ok) return setToast(data.error || "保存失败，请重试");
    setAssets((current) => [...current, data.asset]);
    setModalOpen(false);
    setToast("资产已加入总览");
  }

  async function syncMarketRates() {
    setSyncing(true);
    const next = await Promise.all(assets.map(async (asset) => {
      if (!asset.code || !["stock", "fund", "money"].includes(asset.category)) return asset;
      try {
        const response = await fetch(`/api/market?code=${encodeURIComponent(asset.code)}&category=${asset.category}&days=${lookback * 365}`);
        const data = await response.json();
        return response.ok ? { ...asset, annual_rate: Number(data.annualRate.toFixed(2)) } : asset;
      } catch { return asset; }
    }));
    setAssets(next);
    setSyncing(false);
    setToast(`已按最近 ${lookback} 年数据更新预测率`);
  }

  async function removeAsset(asset: Asset) {
    const response = await fetch(`/api/assets?id=${asset.id}`, { method: "DELETE" });
    if (!response.ok) return setToast("删除失败，请重试");
    setAssets((current) => current.filter((item) => item.id !== asset.id));
    setSelected(null);
    setToast("资产已移除");
  }

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
          <button className="icon-button" aria-label="通知">●</button>
          <button className="avatar" aria-label="个人中心">林</button>
        </div>
      </header>

      <section className="content" id="top">
        <div className="welcome-row">
          <div>
            <p className="eyebrow">2026年8月2日 · 资产总览</p>
            <h1>晚上好，看看财富生长到哪里了。</h1>
          </div>
          <button className="primary-button" onClick={() => setModalOpen(true)}><span>＋</span> 记录资产</button>
        </div>

        <section className="summary-grid" id="overview">
          <article className="total-card">
            <div className="card-label"><span>总资产</span><span className="status-dot">数据已保存</span></div>
            <div className="total-value">{money(total)}</div>
            <div className="change-row"><span className="change-pill">↑ 0.95%</span><span>本月预估增长 {money(expectedGain / Math.max(1, horizon * 12))}</span></div>
            <div className="mini-stats">
              <div><span>可产生收益</span><strong>{money(total - (grouped.find((g) => g.category === "fixed")?.amount || 0))}</strong></div>
              <div><span>组合预期年化</span><strong>{weightedRate.toFixed(2)}%</strong></div>
            </div>
          </article>

          <article className="allocation-card">
            <div className="card-heading"><div><span className="card-kicker">资产配置</span><h2>钱放在了哪里</h2></div><button className="text-button" onClick={() => setActiveFilter("all")}>查看全部</button></div>
            <div className="allocation-body">
              <div className="donut" style={{ background: `conic-gradient(${grouped.map((item, index) => {
                const before = grouped.slice(0, index).reduce((sum, group) => sum + group.amount, 0) / total * 100;
                const after = before + item.amount / total * 100;
                return `${categoryMeta[item.category].color} ${before}% ${after}%`;
              }).join(",")})` }}><div><strong>{grouped.length}</strong><span>类资产</span></div></div>
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
            <div className="forecast-number">{money(forecast)}</div>
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
            <p className="disclaimer">预测基于历史收益率与输入利率，不代表实际收益或投资承诺。</p>
          </div>
        </section>

        <section className="assets-section" id="assets">
          <div className="section-heading"><div><span className="card-kicker">我的资产</span><h2>每一笔，都心中有数</h2></div><span>{assets.length} 项资产</span></div>
          <div className="filter-row">
            <button className={activeFilter === "all" ? "active" : ""} onClick={() => setActiveFilter("all")}>全部</button>
            {(Object.keys(categoryMeta) as Category[]).map((category) => <button className={activeFilter === category ? "active" : ""} key={category} onClick={() => setActiveFilter(category)}>{categoryMeta[category].name}</button>)}
          </div>
          <div className="asset-list">
            {filtered.map((asset) => {
              const meta = categoryMeta[asset.category];
              return <button className="asset-row" key={asset.id} onClick={() => setSelected(asset)}>
                <span className="asset-icon" style={{ background: `${meta.color}18`, color: meta.color }}>{meta.short}</span>
                <span className="asset-main"><strong>{asset.name}</strong><small>{meta.name}{asset.code ? ` · ${asset.code}` : ""} · {asset.note}</small></span>
                <span className="asset-rate"><small>{asset.category === "fixed" ? "不计收益" : "预测年化"}</small><strong className={asset.annual_rate < 0 ? "negative" : ""}>{asset.category === "fixed" ? "—" : `${asset.annual_rate.toFixed(2)}%`}</strong></span>
                <span className="asset-amount"><strong>{money(asset.amount)}</strong><small>{total ? (asset.amount / total * 100).toFixed(1) : 0}%</small></span>
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
          <span className="card-kicker">新增记录</span><h2 id="modal-title">把一项资产放进账本</h2><p>录入市值；股票、基金会尝试按代码读取历史收益率。</p>
          <AssetForm onSubmit={addAsset} saving={saving} />
        </section>
      </div>}

      {selected && <div className="modal-backdrop" onMouseDown={(event) => event.target === event.currentTarget && setSelected(null)}>
        <aside className="detail-panel" role="dialog" aria-modal="true">
          <button className="modal-close" onClick={() => setSelected(null)} aria-label="关闭">×</button>
          <span className="asset-icon large" style={{ background: `${categoryMeta[selected.category].color}18`, color: categoryMeta[selected.category].color }}>{categoryMeta[selected.category].short}</span>
          <span className="card-kicker">{categoryMeta[selected.category].name}</span><h2>{selected.name}</h2><p>{selected.note || "暂无备注"}</p>
          <dl><div><dt>当前市值</dt><dd>{money(selected.amount)}</dd></div><div><dt>预测年化</dt><dd>{selected.category === "fixed" ? "不计收益" : `${selected.annual_rate.toFixed(2)}%`}</dd></div>{selected.code && <div><dt>资产代码</dt><dd>{selected.code}</dd></div>}<div><dt>{horizon} 年后预计</dt><dd>{money(selected.amount * Math.pow(1 + selected.annual_rate / 100, horizon))}</dd></div></dl>
          <button className="danger-button" onClick={() => removeAsset(selected)}>删除这项资产</button>
        </aside>
      </div>}

      {toast && <div className="toast" role="status">{toast}</div>}
    </main>
  );
}

function AssetForm({ onSubmit, saving }: { onSubmit: (event: FormEvent<HTMLFormElement>) => void; saving: boolean }) {
  const [category, setCategory] = useState<Category>("stock");
  const needsCode = ["stock", "fund", "money"].includes(category);
  const needsRate = ["deposit", "housing"].includes(category);
  return <form className="asset-form" onSubmit={onSubmit}>
    <label><span>资产类型</span><select name="category" value={category} onChange={(event) => setCategory(event.target.value as Category)}>{(Object.keys(categoryMeta) as Category[]).map((key) => <option value={key} key={key}>{categoryMeta[key].name}</option>)}</select></label>
    <label><span>资产名称</span><input required name="name" placeholder={category === "fixed" ? "例如：自住房产" : "例如：沪深300指数基金"} /></label>
    {needsCode && <label><span>股票 / 基金代码</span><input required name="code" inputMode="numeric" placeholder="输入 6 位代码" maxLength={12} /><small>保存时会读取历史数据，失败时可稍后同步</small></label>}
    <div className="form-two"><label><span>当前市值（元）</span><input required name="amount" type="number" min="0.01" step="0.01" placeholder="100000" /></label>{needsRate && <label><span>年利率（%）</span><input required name="annualRate" type="number" step="0.01" min="0" placeholder="2.60" /></label>}</div>
    <label><span>备注</span><input name="note" placeholder="可选，例如到期日或用途" /></label>
    <button className="primary-button submit" disabled={saving}>{saving ? "正在保存…" : "确认记录"}</button>
  </form>;
}

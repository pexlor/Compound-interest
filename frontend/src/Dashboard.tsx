// 资产仪表盘：通过独立导航视图展示总览、持仓、月收入、资产预测和历史。

"use client";

import { DataExport } from "./DataExport";
import { IncomePlanner } from "./IncomePlanner";
import type { IncomeSettings, IncomeInput } from "./income";

import { FormEvent, memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { calculatePortfolio } from "./portfolio";
import { ForecastPanel } from "./ForecastPanel";
import { canApplyForecast, forecastURL, readForecastScenario, sameStoredAssets, sameForecastRates } from "./forecast";
import type { ForecastResponse, ForecastScenario } from "./forecast";
import { exchangePairs } from "./exchange-rates";
import { mutationHeaders } from "./mutations";
import { mergeMarketResults, missingMarketRates, needsMarketRate, shouldApplyMarketResponse } from "./market-cache";

// Category 列出仪表盘支持的资产类别。
type Category = "stock" | "fund" | "money" | "deposit" | "housing" | "fixed";
// Currency 列出前端支持的资产与收入币种。
type Currency = "CNY" | "USD" | "HKD" | "EUR" | "JPY" | "GBP" | "SGD" | "AUD" | "CAD" | "CHF";
// MarketReturnMeta 描述行情价格、收益率、历史覆盖区间和缓存状态。
type MarketReturnMeta = {
  annualRate: number;
  annualReady?: boolean;
  pending?: boolean;
  fetchedAt?: string;
  error?: string;
  category?: string;
  code?: string;
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
// Asset 表示前端持仓数据及关联行情，金额以最小货币单位记录。
type Asset = {
  id: number;
  version: number;
  name: string;
  category: Category;
  code: string | null;
  amount: number;
  quantity: number | null;
  currency: Currency;
  annual_rate: number;
  note: string;
  created_at: string;
  market_return?: MarketReturnMeta;
};
// User 表示前端登录用户的基本资料。
type User = { id: number; email: string; displayName: string };
// HistoryEntry 表示某一天的人民币资产快照及其记录时间。
type HistoryEntry = {
  id: number;
  snapshot_date: string;
  total_cny: number;
  trigger: "asset_change" | "exchange_refresh" | "daily_scheduled" | "dashboard_open" | "startup_recovery";
  rate_date: string | null;
};
// RetirementItem 表示退休目标中的一项资产或支出需求。
type RetirementItem = { id: number; version: number; name: string; category: string; amount: number; currency: Currency };
// Retirement 对应退休计划接口的目标、进度、预测时间与明细。
type Retirement = { target_cny: number; current_cny: number; progress: number; projected_years: number | null; projected_date?: string; missing_currencies?: string[]; annual_rate: number; complete?: boolean; liquid_cny?: number; forecast_state?: string; items: RetirementItem[] };

// emptyIncome 构造带初始版本和上海日期的空收入设置。
const emptyIncome = (): IncomeSettings => ({ version: 0, monthly_salary: 0, monthly_savings: 0, annual_bonus: 0, updated_at: null, bonus_settings: null, options: [], cashflows: [], forecast_as_of: new Date().toLocaleDateString("en-CA", { timeZone: "Asia/Shanghai" }) });


const categoryMeta: Record<Category, /* 定义资产类别的中文名称、缩写和展示颜色。 */ { name: string; short: string; color: string }> = {
  stock: { name: "股票", short: "股", color: "#ee6a4d" },
  fund: { name: "基金", short: "基", color: "#a78bfa" },
  money: { name: "货币基金", short: "货", color: "#28a88a" },
  deposit: { name: "存款", short: "存", color: "#e7b344" },
  housing: { name: "公积金", short: "积", color: "#5196e3" },
  fixed: { name: "固定资产", short: "固", color: "#8c98a4" },
};

const assetColors = ["#34775c", "#e4a83f", "#df7259", "#6f8fd6", "#9a76c4", "#3aa6a0", "#c985a2", "#84975a", "#c47b3d", "#70818d"];
const ASSET_PAGE_SIZE = 5;

// AllocationSegment 描述资产分布图中的名称、金额与显示颜色。
type AllocationSegment = { key: string; label: string; amount: number; color: string };

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

const numberFormatters = new Map<string, Intl.NumberFormat>();

// getNumberFormatter 缓存并返回指定格式的中文数字格式化器。
function getNumberFormatter(key: string, options: Intl.NumberFormatOptions) {
  let formatter = numberFormatters.get(key);
  if (!formatter) {
    formatter = new Intl.NumberFormat("zh-CN", options);
    numberFormatters.set(key, formatter);
  }
  return formatter;
}

// money 将以分为单位的金额格式化为人民币。
const money = (cents: number, digits = 0) => getNumberFormatter(`money:${digits}`, {
  style: "currency", currency: "CNY", minimumFractionDigits: digits, maximumFractionDigits: digits,
}).format(cents / 100);

// originalMoney 按资产原始币种格式化金额。
const originalMoney = (cents: number, currency: Currency) => getNumberFormatter(`original:${currency}`, {
  style: "currency", currency, currencyDisplay: "symbol",
  minimumFractionDigits: currency === "JPY" ? 0 : 2,
  maximumFractionDigits: currency === "JPY" ? 0 : 2,
}).format(cents / 100);

// toCny 使用当前汇率将资产市值换算为人民币分。
const toCny = (asset: Asset, rates: Partial<Record<Currency, number>>) =>
  asset.amount * (rates[asset.currency] ?? 0);

// isQuantityAsset 判断资产是否需要记录持有数量。
const isQuantityAsset = (asset: Pick<Asset, "category">) => asset.category === "stock" || asset.category === "fund";

// quantityText 格式化股票或基金的持有数量。
const quantityText = (value: number) => getNumberFormatter("quantity", { maximumFractionDigits: 8 }).format(value);

// priceText 按指定币种格式化单位价格。
const priceText = (value: number, currency: Currency) => getNumberFormatter(`price:${currency}`, {
  style: "currency", currency, maximumFractionDigits: 4,
}).format(value);

// marketKey 生成市场行情缓存的唯一键。
const marketKey = (category: string, code: string) => `${category}:${code.trim().toUpperCase()}`;

// StarMapMark 渲染“资产星图”的品牌星座图标。
function StarMapMark() {
  return <span className="brand-mark" aria-hidden="true"><svg viewBox="0 0 32 32" fill="none">
    <path d="M8 21 15.5 10l8.5 8" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" />
    <path d="m8 21 11.5 3" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" />
    <circle cx="8" cy="21" r="2.5" fill="currentColor" /><circle cx="15.5" cy="10" r="2.5" fill="currentColor" /><circle cx="24" cy="18" r="2.5" fill="currentColor" /><circle cx="19.5" cy="24" r="2.2" fill="currentColor" />
  </svg></span>;
}

// navigationPages 定义各独立页面的地址、导航名称与页面标题。
const navigationPages = [
  { hash: "#overview", label: "总览", title: "看看财富生长到哪里了" },
  { hash: "#assets", label: "资产", title: "管理你的每一笔资产" },
  { hash: "#income", label: "月收入记录", title: "记录工资、年终奖与期权" },
  { hash: "#forecast", label: "资产预测", title: "设定退休目标，查看资产预测" },
  { hash: "#history", label: "历史", title: "回顾资产走过的轨迹" },
] as const;

// readNavigationPage 将有效页面地址映射为独立视图，首页及未知地址返回总览。
function readNavigationPage() {
  return navigationPages.find(/* 按当前地址匹配导航页面。 */ page => page.hash === window.location.hash) ?? navigationPages[0];
}

// Dashboard 承载资产星图的登录态、资产、预测和退休目标界面。
export default function Dashboard() {
  const [activePage, setActivePage] = useState(readNavigationPage);
  const activeNav = activePage.hash;
  useEffect(/* 同步独立页面地址，支持直接打开、刷新及浏览器前进和后退。 */ () => {
    // syncNavigation 按当前地址切换唯一可见页面。
    function syncNavigation() {
      setActivePage(readNavigationPage());
    }
    window.addEventListener("hashchange", syncNavigation);
    return /* 卸载时移除页面导航监听。 */ () => window.removeEventListener("hashchange", syncNavigation);
  }, []);
  useEffect(/* 切换页面后回到顶部，避免沿用上一页面的滚动位置。 */ () => {
    window.scrollTo({ top: 0, behavior: "instant" });
  }, [activeNav]);
  const [user, setUser] = useState<User | null>(null);
  const [authLoading, setAuthLoading] = useState(true);
  const [assetsLoading, setAssetsLoading] = useState(false);
  const [assets, setAssets] = useState<Asset[]>([]);
  const [marketRates, setMarketRates] = useState<Record<string, MarketReturnMeta>>({});
  const [marketPending, setMarketPending] = useState(false);
  const [marketChecked, setMarketChecked] = useState(false);
  const [history, setHistory] = useState<HistoryEntry[]>([]);
  const [income, setIncome] = useState<IncomeSettings>(emptyIncome());
  const [retirement, setRetirement] = useState<Retirement | null>(null);
  const [savingRetirement, setSavingRetirement] = useState(false);
  const [activeFilter, setActiveFilter] = useState<"all" | Category>("all");
  const [assetPage, setAssetPage] = useState(0);
  const [allocationMode, setAllocationMode] = useState<"category" | "asset">("category");
  const [horizon, setHorizon] = useState(10);
  const [forecastData,setForecastData]=useState<ForecastResponse|null>(null);
  const [forecastLoading,setForecastLoading]=useState(false);
  const [forecastError,setForecastError]=useState("");
  const [forecastRevision,setForecastRevision]=useState(0);
  const [scenarioState,setScenarioState]=useState<{owner:number|null;value:ForecastScenario}>({owner:null,value:readForecastScenario(null)});
  const forecastGeneration=useRef(0);
  const activeForecastUser=useRef<number|null>(null);
  activeForecastUser.current=user?.id??null;
  const scenario=scenarioState.value;
  const [lookback, setLookback] = useState(3);
  const activeMarketDays = useRef(1095);
  const marketRequestGeneration = useRef(0);
  activeMarketDays.current = lookback * 365;
  const [modalOpen, setModalOpen] = useState(false);
  const [exportOpen, setExportOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [updating, setUpdating] = useState(false);
  const [savingIncome, setSavingIncome] = useState(false);
  const [toast, setToast] = useState("");
  const [selected, setSelected] = useState<Asset | null>(null);
  const [exchangeRates, setExchangeRates] = useState<Partial<Record<Currency, number>>>({ CNY: 1 });
  const [exchangeDate, setExchangeDate] = useState("");
  const [exchangeLoading, setExchangeLoading] = useState(false);
  const [exchangeStale, setExchangeStale] = useState(false);
  const exchangeOutdated = exchangeStale || Boolean(exchangeDate && exchangeDate !== new Date().toLocaleDateString("sv-SE", { timeZone: "Asia/Shanghai" }));

  // loadMarketRates 拉取并缓存已持仓证券的行情与年化收益率。
  const loadMarketRates = useCallback(async (selectedLookback: number, notify = false, signal?: AbortSignal) => {
    const requestGeneration = ++marketRequestGeneration.current;
    setSyncing(true);
    try {
      const response = await fetch(`/api/market?days=${selectedLookback * 365}${notify ? "&refresh=1" : ""}`, { signal });
      const data = await response.json() as /* 定义批量行情查询响应中的结果列表和逐项错误。 */ { results?: Array<MarketReturnMeta & /* 补充行情结果对应的资产类别与证券代码。 */ { category: string; code: string }>; errors?: unknown[]; error?: string };
      if (response.status === 401) {
        setUser(null);
        return;
      }
      if (!response.ok) throw new Error(data.error || "读取市场收益失败");
      if (!shouldApplyMarketResponse(selectedLookback * 365, activeMarketDays.current, requestGeneration, marketRequestGeneration.current)) return;
      setMarketRates(current => mergeMarketResults(current, data.results ?? [], selectedLookback * 365));
      setMarketPending((data.results ?? []).some(result => result.pending));
      setMarketChecked(true);
      if (notify) {
        const failed = (data.errors?.length ?? 0) + (data.results ?? []).filter(result => result.error && !result.annualReady).length;
        setToast(data.results?.some(result => result.pending) ? "已开始更新行情，完成后自动显示" : failed ? `已读取 ${data.results?.length ?? 0} 项，${failed} 项行情暂不可用` : `已读取 ${data.results?.length ?? 0} 项最新价格与收益率`);
      }
    } catch (error) {
      if (error instanceof DOMException && error.name === "AbortError") return;
      if (notify) setToast(error instanceof Error ? error.message : "读取市场收益失败");
    } finally {
      if (!signal?.aborted && requestGeneration === marketRequestGeneration.current) setSyncing(false);
    }
  }, []);

  useEffect(/* 首次挂载及登录身份变化时读取仪表盘，清理旧请求以避免迟到响应覆盖。 */ () => {
    const controller = new AbortController();
    setRetirement(null);
    fetch("/api/dashboard", { signal: controller.signal })
      .then(/* 校验仪表盘接口状态并解析返回数据，未登录时返回空结果。 */ async (response) => {
        if (response.status === 401) {
          if (!controller.signal.aborted) setUser(null);
          return null;
        }
        const data = await response.json() as /* 定义仪表盘接口返回的用户、持仓、历史、收入和汇率数据。 */ {
          user?: User; assets?: Asset[]; history?: HistoryEntry[]; income?: IncomeSettings;
          rates?: Partial<Record<Currency, number>>; date?: string; stale?: boolean; error?: string; marketResults?: MarketReturnMeta[];
        };
        if (!response.ok) throw new Error(data.error || "读取本地账本失败");
        return data;
      })
      .then(/* 把仪表盘响应写入界面状态，并继续读取退休计划。 */ (dashboard) => {
        if (!dashboard || controller.signal.aborted) return;
        setUser(dashboard.user ?? null);
        setAssets(dashboard.assets ?? []);
        setMarketRates(mergeMarketResults({}, dashboard.marketResults ?? [], 1095));
        setHistory(dashboard.history ?? []);
        setIncome(dashboard.income ?? emptyIncome());
        setExchangeRates(dashboard.rates ?? { CNY: 1 });
        setExchangeDate(dashboard.date ?? "");
        setExchangeStale(Boolean(dashboard.stale));

      })
      .catch(/* 仅处理仍有效的读取失败，取消的旧请求不改变登录态。 */ () => { if (!controller.signal.aborted) setUser(null); })
      .finally(/* 读取失败时清除登录态，让界面回到登录入口。 */ () => {
        if (controller.signal.aborted) return;
        setAssetsLoading(false);
        setAuthLoading(false);
      });
    return /* 登录状态变化或卸载时取消旧的仪表盘请求。 */ () => controller.abort();
  }, [user?.id]);

  // saveRetirement 提交一项新的退休目标资产。
  async function saveRetirement(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setSavingRetirement(true);
    const formElement = event.currentTarget;
    try {
      const form = new FormData(formElement);
      const response = await fetch("/api/retirement", { method: "POST", headers: mutationHeaders(), body: JSON.stringify({ name: form.get("name"), category: form.get("category"), currency: form.get("currency"), amount: Number(form.get("amount")) }) });
      const data = await response.json() as Retirement & /* 补充接口失败时可能返回的错误提示。 */ { error?: string };
      if (!response.ok) throw new Error(data.error || "保存退休目标失败");
      setRetirement(data); setForecastRevision(value=>value+1); formElement.reset(); setToast("退休目标资产已添加");
    } catch (error) { setToast(error instanceof Error ? error.message : "保存退休目标失败"); } finally { setSavingRetirement(false); }
  }
  // removeRetirementItem 删除指定的退休目标资产。
  async function removeRetirementItem(item: RetirementItem) {
    try {
      const response = await fetch("/api/retirement/items", { method: "DELETE", headers: mutationHeaders(), body: JSON.stringify({ id: item.id, version: item.version }) });
      const data = await response.json() as Retirement & /* 补充接口失败时可能返回的错误提示。 */ { error?: string };
      if (!response.ok) throw new Error(data.error || "删除失败");
      setRetirement(data); setForecastRevision(value=>value+1);
    } catch (error) { setToast(error instanceof Error ? error.message : "删除失败"); }
  }

  useEffect(/* 登录后按回溯年限延迟读取行情，并在依赖变化时清理请求。 */ () => {
    if (!user) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof window.setTimeout>;
    // poll 定期读取当前区间缓存，后台补齐时缩短检查间隔。
    const poll = async () => {
      await loadMarketRates(lookback, false, controller.signal);
      if (!controller.signal.aborted) timer = window.setTimeout(poll, marketPending ? 3000 : 60000);
    };
    timer = window.setTimeout(poll, 0);
    return () => { window.clearTimeout(timer); controller.abort(); };
  }, [loadMarketRates, lookback, user, marketPending]);

  useEffect(/* 为临时提示设置自动清除定时器，并在卸载时清理。 */ () => {
    if (!toast) return;
    const timer = window.setTimeout(/* 提示展示到期后清空提示文本。 */ () => setToast(""), 2800);
    return /* 组件清理时取消提示定时器。 */ () => window.clearTimeout(timer);
  }, [toast]);

  const displayAssets = useMemo(/* 缓存合并行情后的展示资产，减少重复处理持仓数据。 */ () => assets.map(/* 合并证券行情与持仓记录，保留没有行情的原资产数据。 */ (asset) => {
    if (!asset.code) return asset;
    const marketReturn = marketRates[marketKey(asset.category, asset.code)];
    if (!marketReturn || marketReturn.requestedDays !== lookback * 365 || marketReturn.annualReady !== true) return asset;
    // 行情加载是异步的；不要在它返回后用“数量 × 最新价”临时改写
    // 已保存的资产金额，否则页面打开后总资产会发生一次视觉跳变。
    // 用户在新增或编辑资产时，才会明确以实时价格保存新的金额。
    return {
      ...asset,
      annual_rate: marketReturn.annualRate,
      market_return: marketReturn,
    };
  }), [assets, marketRates, lookback]);
  // A quote refresh is an enhancement, not a prerequisite for showing the
  // portfolio: each asset already stores its last known market value. Only a
  // genuinely missing currency conversion should block the aggregate view.
  const missingExchangeRate = displayAssets.some(/* 检查相关资产或预测现金流是否缺少有效汇率。 */ (asset) => !exchangeRates[asset.currency]);
  const currentPortfolio = useMemo(/* 仅汇总当前资产，未来预测统一读取后端。 */ () => calculatePortfolio(displayAssets, exchangeRates, 0), [displayAssets, exchangeRates]);
  const total=currentPortfolio?.total??0;
  const weightedRate=currentPortfolio?.weightedRate??0;
  const endForecast=forecastData?.state==="ready"&&!forecastLoading?forecastData.series[forecastData.series.length-1]:null;
  const monthlyForecast=forecastData?.state==="ready"&&!forecastLoading?forecastData.thisMonth:null;
  const selectedForecast=forecastData?.state==="ready"&&!forecastLoading?forecastData.assets.find(/* 查找当前详情资产的本币预测。 */ item=>item.id===selected?.id):undefined;

  useEffect(/* 身份切换后只读取该用户的本机情景，避免旧账号设置影响新账号。 */ ()=>{
    const owner=user?.id??null;
    let raw:null|string=null;
    if(owner!==null)try{raw=localStorage.getItem(`fulibu-forecast:${owner}`);}catch{/* 禁止存储时仍可使用默认情景。 */}
    setScenarioState({owner,value:readForecastScenario(raw)});
  },[user?.id]);
  // changeScenario 保存当前用户本机情景，不写入他人的账号配置。
  const changeScenario=(value:ForecastScenario)=>{
    const owner=user?.id??null;setScenarioState({owner,value});
    if(owner!==null)try{localStorage.setItem(`fulibu-forecast:${owner}`,JSON.stringify(value));}catch{/* 本机存储不可用不影响预测。 */}
  };
  useEffect(/* 只应用当前身份与情景的预测，数据补齐后自动重新读取。 */ ()=>{
    const owner=user?.id??null;
    setForecastData(null);setForecastError("");
    setRetirement(current=>current?{...current,projected_years:null,projected_date:undefined,forecast_state:"pending"}:current);
    if(owner===null||scenarioState.owner!==owner){setForecastLoading(false);return;}
    const controller=new AbortController();let timer:ReturnType<typeof setTimeout>;
    // poll 从唯一后端引擎读取预测与退休，迟到响应和旧会话都不会覆盖当前结果。
    const poll=async()=>{
      const generation=++forecastGeneration.current;setForecastLoading(true);
      let delay=60000;
      try{
        const response=await fetch(forecastURL(horizon,scenario),{signal:controller.signal});
        const value=await response.json() as ForecastResponse&{error?:string};
        if(controller.signal.aborted||!canApplyForecast(owner,activeForecastUser.current,generation,forecastGeneration.current))return;
        if(response.status===401){setUser(null);return;}
        if(!response.ok)throw new Error(value.error||"预测暂不可用");
        setAssets(current=>sameStoredAssets(current,value.storedAssets)?current:value.storedAssets as Asset[]);
        setExchangeRates(current=>sameForecastRates(current,value.rates)?current:value.rates);setExchangeDate(value.rateDate);
        setForecastData(value);setRetirement(value.retirement as Retirement);setForecastError("");
        if(value.state!=="ready")delay=10000;
      }catch(error){if(!controller.signal.aborted){setForecastError(error instanceof Error?error.message:"读取预测失败");delay=15000;}}
      finally{if(!controller.signal.aborted){setForecastLoading(false);timer=setTimeout(poll,delay);}}
    };
    timer=setTimeout(poll,150);
    return /* 取消旧情景或旧身份的请求并清除轮询。 */ ()=>{controller.abort();clearTimeout(timer);++forecastGeneration.current;};
  },[user?.id,scenarioState,horizon,assets,income,exchangeRates,forecastRevision]);

  const grouped = useMemo(/* 缓存各资产类别的人民币金额合计。 */ () => {
    return (Object.keys(categoryMeta) as Category[]).map(/* 计算当前类别下的资产人民币金额合计。 */ (category) => ({
      category,
      amount: displayAssets.filter(/* 筛选属于当前选定类别的资产。 */ (item) => item.category === category).reduce(/* 累计筛选后资产的人民币市值。 */ (sum, item) => sum + toCny(item, exchangeRates), 0),
    })).filter(/* 仅保留金额为正的资产分布项。 */ (item) => item.amount > 0);
  }, [displayAssets, exchangeRates]);
  const assetAllocations = useMemo(/* 缓存按市值排序的资产分布及展示颜色。 */ () => displayAssets.map(/* 生成单项资产的人民币金额和分布图颜色。 */ (asset, index) => ({
    asset,
    amount: toCny(asset, exchangeRates),
    color: assetColors[index % assetColors.length],
  })).filter(/* 仅保留金额为正的资产分布项。 */ (item) => item.amount > 0).sort(/* 按人民币金额从大到小排列资产分布项。 */ (left, right) => right.amount - left.amount), [displayAssets, exchangeRates]);
  const allocationSegments: AllocationSegment[] = allocationMode === "category"
    ? grouped.map(/* 将类别汇总转换为分布图的名称、金额和颜色。 */ (item) => ({ key: item.category, label: categoryMeta[item.category].name, amount: item.amount, color: categoryMeta[item.category].color }))
    : assetAllocations.map(/* 将单项资产转换为分布图的名称、金额和颜色。 */ (item) => ({ key: String(item.asset.id), label: item.asset.name, amount: item.amount, color: item.color }));
  let allocationBefore = 0;
  const allocationGradient = total
    ? `conic-gradient(${allocationSegments.map(/* 累计各分布项的起止比例，生成环形图的颜色区间。 */ (item) => {
      const start = allocationBefore / total * 100;
      allocationBefore += item.amount;
      return `${item.color} ${start}% ${allocationBefore / total * 100}%`;
    }).join(",")})`
    : "#edf1ee";

  const filtered = activeFilter === "all" ? displayAssets : displayAssets.filter(/* 筛选属于当前选定类别的资产。 */ (asset) => asset.category === activeFilter);
  const assetPageCount = Math.max(1, Math.ceil(filtered.length / ASSET_PAGE_SIZE));
  const currentAssetPage = Math.min(assetPage, assetPageCount - 1);
  const pagedAssets = filtered.slice(currentAssetPage * ASSET_PAGE_SIZE, currentAssetPage * ASSET_PAGE_SIZE + ASSET_PAGE_SIZE);
  const missingHistoricalRates = missingMarketRates(assets, marketRates, lookback * 365).length > 0;
  const forecastRateStatus = !marketChecked || marketPending || syncing ? "正在计算" : "年化暂不可用";
  const limitedHistoryCount = displayAssets.filter(/* 筛选历史价格覆盖不足的证券资产。 */ (asset) => asset.market_return?.historyLimited).length;
  const selectedMarket = selected ? displayAssets.find(/* 从最新展示资产中查找当前选中资产。 */ (asset) => asset.id === selected.id) ?? selected : null;


  // refreshHistory 重新读取资产历史走势数据。
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

  // saveIncome 保存工资、储蓄、年终奖与期权安排，并刷新收入和退休预测。
  async function saveIncome(input: IncomeInput) {
    setSavingIncome(true);
    try {
      const response = await fetch("/api/income", {
        method: "PATCH",
        headers: mutationHeaders(),
        body: JSON.stringify(input),
      });
      const data = await response.json();
      if (response.status === 401) {
        setUser(null);
        return;
      }
      if (!response.ok) throw new Error(data.error || "保存工资设置失败");
      setIncome(data.income);
      setToast("收入与期权归属计划已保存，预测已更新");
    } catch (error) {
      setToast(error instanceof Error ? error.message : "保存工资设置失败");
    } finally {
      setSavingIncome(false);
    }
  }

  // addAsset 查询必要行情后新增资产。
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
      const quantity = quantityAsset ? Number(form.get("quantity")) : undefined;
      if (code && ["stock", "fund", "money"].includes(category)) {
        const marketResponse = await fetch(`/api/market?code=${encodeURIComponent(code)}&category=${category}&days=${lookback * 365}`);
        const market = await marketResponse.json();
        if (!marketResponse.ok && quantityAsset) throw new Error(market.error || "暂时无法读取实时价格");
        if (marketResponse.ok) {
          if (market.annualReady === true) rate = Number(market.annualRate.toFixed(2));
          marketReturn = market as MarketReturnMeta;
        }
      }
      if (quantityAsset && (!marketReturn?.currentPrice || !marketReturn.priceCurrency)) throw new Error("暂时无法读取实时价格，请稍后重试");
      const inputAmount = quantityAsset ? quantity! * marketReturn!.currentPrice! : Number(form.get("amount"));
      const inputCurrency = quantityAsset ? marketReturn!.priceCurrency! : form.get("currency");
      const response = await fetch("/api/assets", {
        method: "POST",
        headers: mutationHeaders(),
        body: JSON.stringify({
          name: form.get("name"), category, code,
          amount: inputAmount, quantity, currency: inputCurrency, annualRate: rate, note: form.get("note"),
        }),
      });
      const data = await response.json();
      if (response.status === 401) {
        setUser(null);
        return;
      }
      if (!response.ok) return setToast(data.error || "保存失败，请重试");
      setAssets(/* 把新建资产追加到最新持仓状态中。 */ (current) => [...current, data.asset]);
      if (marketReturn?.annualReady === true) setMarketRates(/* 将最新行情合并到现有缓存，保留其他证券的数据。 */ (current) => ({ ...current, [marketKey(category, code)]: marketReturn! }));
      await refreshHistory();
      setModalOpen(false);
      setToast(data.snapshot ? "资产已加入总览，今日历史已更新" : "资产已加入；汇率不完整，今日历史暂未更新");
    } catch (error) {
      setToast(error instanceof Error ? error.message : "保存失败，请检查本地服务后重试");
    } finally {
      setSaving(false);
    }
  }

  // syncMarketRates 由用户主动刷新已持仓证券的行情。
  async function syncMarketRates() {
    await loadMarketRates(lookback, true);
  }

  // refreshExchangeRates 强制刷新外币兑人民币汇率。
  async function refreshExchangeRates() {
    setExchangeLoading(true);
    try {
      // This action is explicitly user initiated, so ask the server to refresh
      // rather than merely returning a same-day cache entry.
      const response = await fetch("/api/exchange-rates?refresh=1");
      const data = await response.json();
      if (!response.ok) throw new Error(data.error || "读取最新汇率失败");
      setExchangeRates(data.rates);
      setExchangeDate(data.date);
      setExchangeStale(Boolean(data.stale));
      await refreshHistory();
      setToast(data.stale ? "汇率缓存暂不可用" : `已读取 ${data.date} 的汇率缓存`);
    } catch (error) {
      setToast(error instanceof Error ? error.message : "读取最新汇率失败");
    } finally {
      setExchangeLoading(false);
    }
  }

  // removeAsset 归档资产并同步刷新走势数据。
  async function removeAsset(asset: Asset) {
    const response = await fetch("/api/assets/archive", { method: "POST", headers: mutationHeaders(), body: JSON.stringify({ id: asset.id, version: asset.version }) });
    if (response.status === 401) {
      setUser(null);
      return;
    }
    const data = await response.json();
    if (!response.ok) return setToast(data.error || "归档失败，请重试");
    setAssets(/* 从最新持仓状态中移除已归档资产。 */ (current) => current.filter(/* 排除已经归档的资产记录。 */ (item) => item.id !== asset.id));
    await refreshHistory();
    setSelected(null);
    setToast(data.snapshot ? "资产已移除，今日历史已更新" : "资产已移除，今日历史暂未更新");
  }

  // updateAsset 保存编辑后的资产市值、数量与币种。
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
        setMarketRates(/* 将最新行情合并到现有缓存，保留其他证券的数据。 */ (current) => ({ ...current, [marketKey(selected.category, selected.code!)]: market }));
      }
      const response = await fetch("/api/assets", {
        method: "PATCH",
        headers: mutationHeaders(),
        body: JSON.stringify({
          id: selected.id, version: selected.version,
          amount: quantityAsset ? quantity! * currentPrice! : Number(form.get("amount")),
          quantity,
          currency: quantityAsset ? currency : form.get("currency"),
        }),
      });
      const data = await response.json();
      if (response.status === 401) {
        setUser(null);
        return;
      }
      if (!response.ok) return setToast(data.error || "修改失败，请重试");
      const updated = data.after as Asset;
      setAssets(/* 用接口返回的新记录替换同编号的资产，保留其他持仓。 */ (current) => current.map(/* 用接口返回的新记录替换同编号的资产，保留其他持仓。 */ (asset) => asset.id === updated.id ? updated : asset));
      setSelected(updated);
      await refreshHistory();
      setToast(data.snapshot ? `${quantityAsset ? "持有数量" : "资产金额与币种"}已保存，今日历史已更新` : "资产已保存，今日历史暂未更新");
    } catch (error) {
      setToast(error instanceof Error ? error.message : "修改失败，请重试");
    } finally {
      setUpdating(false);
    }
  }

  // logout 结束当前会话并清空前端状态。
  async function logout() {
    await fetch("/api/auth/logout", { method: "POST" });
    setUser(null);
    setAssets([]);
    setMarketRates({});
    setHistory([]);
    setIncome(emptyIncome());
    setAssetsLoading(false);
    setSelected(null);
    setExchangeRates({ CNY: 1 });
    setExchangeDate("");
  }

  if (authLoading) return <LoadingScreen label="正在打开本地账本…" />;
  if (!user) return <AuthScreen onAuthenticated={/* 认证成功后保存用户信息，并进入资产加载状态。 */ (authenticatedUser) => {
    setAssetsLoading(true);
    setUser(authenticatedUser);
  }} />;
  if (assetsLoading) return <LoadingScreen label="正在读取你的资产…" />;

  return (
    <main className="app-shell">
      <header className="topbar">
        <a className="brand" href="#overview" aria-label="资产星图首页">
          <StarMapMark /><span>资产星图</span>
        </a>
        <nav aria-label="主要导航">
          {navigationPages.map(/* 渲染独立页面入口，并标记当前所在页面。 */ page => <a key={page.hash} className={activeNav === page.hash ? "nav-active" : ""} aria-current={activeNav === page.hash ? "page" : undefined} href={page.hash}>{page.label}</a>)}
        </nav>
        <div className="header-actions">
          <div className="user-chip"><span className="avatar">{user.displayName.slice(0, 1)}</span><span className="user-meta"><strong>{user.displayName}</strong><small>{user.email}</small></span></div>
          <button className="logout-button" onClick={/* 打开数据导出选择窗口。 */ () => setExportOpen(true)}>数据导出</button>
          <button className="logout-button" onClick={logout}>退出</button>
        </div>
      </header>

      {exportOpen && <DataExport assets={assets} onClose={/* 关闭数据导出窗口。 */ () => setExportOpen(false)} />}

      <section className="content" id="top">
        <div className="welcome-row">
          <div>
            <p className="eyebrow">{new Date().toLocaleDateString("zh-CN", { year: "numeric", month: "long", day: "numeric", timeZone: "Asia/Shanghai" })} · {activePage.label}</p>
            <h1>{user.displayName}，{activePage.title}</h1>
          </div>
          <button className="primary-button" onClick={/* 更新新增资产弹窗的显示状态。 */ () => setModalOpen(true)}><span>＋</span> 记录资产</button>
        </div>

        <section className="summary-grid" id="overview" hidden={activeNav !== "#overview"} aria-label="资产总览">
          <article className="total-card">
            <div className="card-label"><span>总资产 · 折合人民币</span><button className={`exchange-status${exchangeOutdated ? " stale" : ""}`} onClick={refreshExchangeRates} disabled={exchangeLoading}>{exchangeLoading ? "正在读取汇率…" : exchangeDate ? `${exchangeDate}${exchangeOutdated ? " · 旧数据" : ""} · 刷新汇率` : "读取汇率缓存"}</button></div>
            <div className="total-card-main">
              <div className="total-card-amount">
                <div className="total-value">{missingExchangeRate ? "行情或汇率暂不可用" : money(total)}</div>
                <div className="change-row"><span className="change-pill">汇率折算</span><span title={monthlyForecast?`${forecastData?.asOf} 至 ${monthlyForecast.date}：投资收益 ${money(monthlyForecast.investmentGain)} + 到账本金 ${money(monthlyForecast.contributions)}`:undefined}>本月剩余预估增长 {monthlyForecast?money(monthlyForecast.totalGain):forecastLoading?"正在计算":"等待预测数据"}</span><small className="month-breakdown">{monthlyForecast?`投资收益 ${money(monthlyForecast.investmentGain)} · 到账本金 ${money(monthlyForecast.contributions)}`:""}</small></div>
              </div>
              <aside className="exchange-panel" aria-label="人民币、美元、港元双向汇率" aria-live="polite">
                <span className="exchange-panel-title">{exchangeDate && !exchangeOutdated ? "今日汇率" : "最新可用汇率"}</span>
                {exchangePairs(exchangeRates).map(/* 展示每组货币的正向和反向汇率，缺失数据时显示等待状态。 */ ({ base, quote, forward, reverse }) => <div className="exchange-pair" key={`${base}-${quote}`}>
                  <strong>{base === "HKD" ? "港元" : currencyMeta[base]} ↔ {quote === "HKD" ? "港元" : currencyMeta[quote]}</strong>
                  {forward === null || reverse === null ? <span>等待汇率</span> : <><span>1 {base} = {forward.toFixed(4)} {quote}</span><span>1 {quote} = {reverse.toFixed(4)} {base}</span></>}
                </div>)}
              </aside>
            </div>
            <div className="mini-stats">
              <div><span>可产生收益</span><strong>{missingExchangeRate ? "等待汇率" : money(total - (grouped.find(/* 查找固定资产分组，用于显示固定资产总额。 */ (g) => g.category === "fixed")?.amount || 0))}</strong></div>
              <div><span>组合历史年化（近{lookback}年）</span><strong>{missingExchangeRate ? "等待汇率" : missingHistoricalRates ? forecastRateStatus : `${weightedRate.toFixed(2)}%`}</strong><small className="portfolio-rate-note">{limitedHistoryCount ? `其中 ${limitedHistoryCount} 项历史不足` : "\u00a0"}</small></div>
            </div>
          </article>

          <article className="allocation-card">
            <div className="card-heading"><div><span className="card-kicker">资产配置</span><h2>钱放在了哪里</h2></div><div className="allocation-switch" aria-label="资产配置展示方式"><button className={allocationMode === "category" ? "active" : ""} onClick={/* 更新资产分布的展示方式。 */ () => setAllocationMode("category")}>按类别</button><button className={allocationMode === "asset" ? "active" : ""} onClick={/* 更新资产分布的展示方式。 */ () => setAllocationMode("asset")}>按资产</button></div></div>
            <div className="allocation-body">
              {!missingExchangeRate ? <div className="donut" style={{ background: allocationGradient }}>
                <div className="donut-center"><strong>{allocationSegments.length}</strong><span>{allocationMode === "category" ? "类资产" : "项资产"}</span></div>
              </div> : <div className="unavailable-state">等待完整汇率后显示配置</div>}
              {!missingExchangeRate && <div className={`allocation-list${allocationMode === "asset" ? " detailed" : ""}`}>
                {allocationSegments.map(/* 渲染资产分布项的交互按钮，允许筛选类别或选择资产。 */ (item) => <button key={item.key} title={item.label} onClick={/* 更新资产类别筛选与选中的资产。 */ () => {
                  if (allocationMode === "category") {
                    setActiveFilter(item.key as Category);
                    setAssetPage(0);
                    window.location.hash = "#assets";
                  }
                  else setSelected(assetAllocations.find(/* 根据分布图选项找到对应的资产。 */ (allocation) => String(allocation.asset.id) === item.key)?.asset ?? null);
                  }}>
                  <span className="legend-dot" style={{ background: item.color }} />
                  <span>{item.label}</span><strong>{total ? (item.amount / total * 100).toFixed(1) : "0.0"}%</strong>
                </button>)}
              </div>}
            </div>
          </article>
        </section>

        <section className="income-page" id="income" hidden={activeNav !== "#income"} aria-label="月收入记录">
          <div className="section-heading"><div><span className="card-kicker">月收入记录</span><h2>工资、年终奖与期权</h2></div></div>
          <p className="page-description">记录月工资与预计储蓄，安排年终奖和期权到账；保存后用于资产预测。</p>
          <IncomePlanner key={`${user?.id}:${income.version}`} income={income} saving={savingIncome} onSave={saveIncome} />
        </section>

        <section className="forecast-card" id="forecast" hidden={activeNav !== "#forecast"} aria-label="资产预测">
          <div className="forecast-copy">
            <span className="card-kicker">稳健资产情景</span>
            <h2>{horizon} 年后，预计拥有</h2>
            <div className="forecast-number">{endForecast?money(endForecast.p50):forecastLoading?"正在计算":"预测暂不可用"}</div>
            <p>显示模拟中位数。仅现有资产计算收益，未来新增资金只计本金。</p>
            <form className="retirement-form" onSubmit={saveRetirement}>
              <div><span className="card-kicker">退休目标资产</span><strong>{retirement?.complete===false?"等待完整汇率":retirement?.target_cny ? `${retirement.progress.toFixed(1)}% 已完成` : "添加退休后希望拥有的资产"}</strong>
                {retirement?.target_cny ? <small>当前可用 {money(retirement.liquid_cny??retirement.current_cny)} / 今日购买力目标 {money(retirement.target_cny)} · {retirement.forecast_state === "pending" ? (forecastError || "正在按当前情景计算") : retirement.projected_years === null ? (retirement.missing_currencies?.length ? `等待汇率：${retirement.missing_currencies.join("、")}` : `所选${horizon}年内未形成可用的达标中位日期`) : retirement.projected_years === 0 ? "已达成" : `首次达标中位日期 ${retirement.projected_date ?? ""}（${retirement.projected_years.toFixed(1)} 年后）`}</small> : <small>仅现有资产计算收益；未来储蓄、年终奖及期权按到账日期计入本金；全部已记录资产均计入退休目标，固定资产按当前估值保持不变。</small>}</div>
              <label><span>资产类型</span><select name="category" defaultValue="deposit"><option value="deposit">存款</option><option value="fund">基金</option><option value="stock">股票</option><option value="housing">房产</option><option value="fixed">其他资产</option></select></label>
              <label><span>目标资产名称</span><input name="name" required placeholder="例如：养老年金" /></label><label><span>金额</span><input name="amount" type="number" min="0.01" step="0.01" required placeholder="例如 1000000" /></label><label><span>币种</span><select name="currency" defaultValue="CNY"><option>CNY</option><option>USD</option><option>HKD</option><option>EUR</option></select></label>
              <button disabled={savingRetirement}>{savingRetirement ? "添加中…" : "添加目标资产"}</button>
              {(retirement?.items ?? []).length > 0 && <ul className="retirement-items">{retirement!.items.map(/* 删除当前退休目标明细，并更新计划进度。 */ item=><li key={item.id}><span>{item.name} · {item.category}</span><b>{money(item.currency === "CNY" ? item.amount : 0)} {item.currency}</b><button type="button" onClick={/* 删除当前退休目标明细，并更新计划进度。 */ ()=>void removeRetirementItem(item)}>删除</button></li>)}</ul>}
            </form>
            <div className="control-block">
              <span>预测到未来</span>
              <div className="segmented">{[1, 3, 5, 10, 20, 30].map(/* 为每个可选预测年限渲染切换按钮。 */ (year) => <button className={horizon === year ? "active" : ""} key={year} onClick={/* 更新收益预测年限。 */ () => setHorizon(year)}>{year}年</button>)}</div>
            </div>
            <div className="sync-row">
              <label>历史年化展示区间<select value={lookback} onChange={(event) => { setLookback(Number(event.target.value)); setMarketChecked(false); }}><option value="1">近1年</option><option value="3">近3年</option><option value="5">近5年</option><option value="10">近10年</option></select></label>
              <button onClick={syncMarketRates} disabled={syncing}>{syncing ? "读取中…" : "刷新价格与收益率"}</button>
            </div>
          </div>
          <ForecastPanel key={user.id} data={forecastData} loading={forecastLoading} error={forecastError} assets={assets} scenario={scenario} onChange={changeScenario} years={horizon} />
        </section>

        <div hidden={activeNav !== "#history"}>
          <HistorySection history={history} />
        </div>

        <section className="assets-section" id="assets" hidden={activeNav !== "#assets"} aria-label="我的资产">
          <div className="section-heading"><div><span className="card-kicker">我的资产</span><h2>每一笔，都心中有数</h2></div><div className="section-actions"><span>{assets.length} 项资产</span><button className="primary-button compact" onClick={/* 更新新增资产弹窗的显示状态。 */ () => setModalOpen(true)}><span>＋</span> 记录资产</button></div></div>
          <div className="filter-row">
            <button className={activeFilter === "all" ? "active" : ""} onClick={/* 更新资产类别筛选与资产列表页码。 */ () => { setActiveFilter("all"); setAssetPage(0); }}>全部</button>
            {(Object.keys(categoryMeta) as Category[]).map(/* 为每个资产类别渲染筛选按钮。 */ (category) => <button className={activeFilter === category ? "active" : ""} key={category} onClick={/* 更新资产类别筛选与资产列表页码。 */ () => { setActiveFilter(category); setAssetPage(0); }}>{categoryMeta[category].name}</button>)}
          </div>
          <div className="asset-list">
            {filtered.length === 0 && <div className="empty-assets"><span>＋</span><h3>{activeFilter === "all" ? "账本还是空的" : `还没有${categoryMeta[activeFilter as Category].name}资产`}</h3><p>从记录第一项资产开始，慢慢建立属于你的全资产视图。</p><button className="primary-button" onClick={/* 更新新增资产弹窗的显示状态。 */ () => setModalOpen(true)}>记录第一项资产</button></div>}
            {pagedAssets.map(/* 渲染资产分布项的交互按钮，允许筛选类别或选择资产。 */ (asset) => {
              const meta = categoryMeta[asset.category];
              const cnyAmount = toCny(asset, exchangeRates);
              return <button className="asset-row" key={asset.id} onClick={/* 更新选中的资产。 */ () => setSelected(asset)}>
                <span className="asset-icon" style={{ background: `${meta.color}18`, color: meta.color }}>{meta.short}</span>
                <span className="asset-main"><strong>{asset.name}</strong><small>{meta.name}{asset.code ? ` · ${asset.code}` : ""}{asset.quantity ? ` · ${quantityText(asset.quantity)} ${asset.category === "stock" ? "股" : "份"}` : ""} · {asset.note}</small></span>
                <span className="asset-rate">
                  <small>{asset.category === "fixed" ? "不计收益" : "历史/输入年化"}</small>
                  <strong className={asset.annual_rate < 0 ? "negative" : ""}>{asset.category === "fixed" ? "—" : needsMarketRate(asset) && !asset.market_return ? forecastRateStatus : `${asset.annual_rate.toFixed(2)}%`}</strong>
                  <em className={asset.market_return?.historyLimited ? "asset-rate-note limited" : "asset-rate-note"}>{asset.market_return?.stale ? "旧数据 · " : ""}{asset.market_return?.historyLimited ? <>历史不足，使用 {asset.market_return.actualDays} 天的数据计算</> : asset.market_return ? `数据截至 ${asset.market_return.endDate}` : "\u00a0"}</em>
                </span>
                <span className="asset-amount"><strong>{originalMoney(asset.amount, asset.currency)}</strong><small>{asset.quantity && asset.market_return?.currentPrice ? `${quantityText(asset.quantity)} × ${priceText(asset.market_return.currentPrice, asset.currency)}` : asset.currency === "CNY" ? "人民币" : exchangeRates[asset.currency] ? `≈ ${money(cnyAmount)} · ${currencyMeta[asset.currency]}` : "等待汇率"}{!missingExchangeRate && total && cnyAmount ? ` · ${(cnyAmount / total * 100).toFixed(1)}%` : ""}</small></span>
                <span className="chevron">›</span>
              </button>;
            })}
          </div>
          {assetPageCount > 1 && <div className="asset-pagination">
            <button type="button" onClick={/* 更新资产列表页码。 */ () => setAssetPage(/* 限制翻页范围，计算上一页或下一页的有效页码。 */ (page) => Math.max(0, page - 1))} disabled={currentAssetPage === 0}>上一页</button>
            <span>第 {currentAssetPage + 1} / {assetPageCount} 页</span>
            <button type="button" onClick={/* 更新资产列表页码。 */ () => setAssetPage(/* 限制翻页范围，计算上一页或下一页的有效页码。 */ (page) => Math.min(assetPageCount - 1, page + 1))} disabled={currentAssetPage >= assetPageCount - 1}>下一页</button>
          </div>}
        </section>
      </section>

      <footer><span><b>资产星图</b> · 看清资产，也看见时间的力量</span><span>数据仅作个人资产记录与测算参考</span></footer>

      {modalOpen && <div className="modal-backdrop" onMouseDown={/* 仅点击遮罩本身时关闭弹窗，避免弹窗内部点击触发关闭。 */ (event) => event.target === event.currentTarget && setModalOpen(false)}>
        <section className="modal" role="dialog" aria-modal="true" aria-labelledby="modal-title">
          <button className="modal-close" onClick={/* 更新新增资产弹窗的显示状态。 */ () => setModalOpen(false)} aria-label="关闭">×</button>
          <span className="card-kicker">新增记录</span><h2 id="modal-title">把一项资产放进账本</h2><p>股票按股数、基金按份数记录，市值会读取最新价格自动计算。</p>
          <AssetForm onSubmit={addAsset} saving={saving} />
        </section>
      </div>}

      {selectedMarket && <div className="modal-backdrop" onMouseDown={/* 仅点击遮罩本身时关闭弹窗，避免弹窗内部点击触发关闭。 */ (event) => event.target === event.currentTarget && setSelected(null)}>
        <aside className="detail-panel" role="dialog" aria-modal="true">
          <button className="modal-close" onClick={/* 更新选中的资产。 */ () => setSelected(null)} aria-label="关闭">×</button>
          <span className="asset-icon large" style={{ background: `${categoryMeta[selectedMarket.category].color}18`, color: categoryMeta[selectedMarket.category].color }}>{categoryMeta[selectedMarket.category].short}</span>
          <span className="card-kicker">{categoryMeta[selectedMarket.category].name}</span><h2>{selectedMarket.name}</h2><p>{selectedMarket.note || "暂无备注"}</p>
          <form className="asset-edit-form" key={`${selectedMarket.id}-${selectedMarket.amount}-${selectedMarket.quantity}-${selectedMarket.currency}`} onSubmit={updateAsset}>
            <div className="edit-heading"><strong>修改资产</strong><span>{isQuantityAsset(selectedMarket) ? "修改数量后按最新价格重新计算市值" : "仅可修改币种和当前市值"}</span></div>
            {isQuantityAsset(selectedMarket)
              ? <label><span>持有{selectedMarket.category === "stock" ? "股数" : "份数"}</span><input required name="quantity" type="number" min="0.00000001" step="any" defaultValue={selectedMarket.quantity ?? ""} /></label>
              : <div className="form-two"><label><span>计价币种</span><select name="currency" defaultValue={selectedMarket.currency}>{(Object.keys(currencyMeta) as Currency[]).map(/* 为币种或资产类别渲染下拉选项。 */ (code) => <option value={code} key={code}>{currencyMeta[code]} · {code}</option>)}</select></label><label><span>当前市值</span><input required name="amount" type="number" min="0.01" step="0.01" defaultValue={(selectedMarket.amount / 100).toFixed(2)} /></label></div>}
            <button className="save-edit-button" disabled={updating}>{updating ? "正在保存…" : "保存修改"}</button>
          </form>
          <dl>{selectedMarket.quantity && <div><dt>持有数量</dt><dd>{quantityText(selectedMarket.quantity)} {selectedMarket.category === "stock" ? "股" : "份"}</dd></div>}{selectedMarket.market_return?.currentPrice && <div><dt>最新价格</dt><dd>{priceText(selectedMarket.market_return.currentPrice, selectedMarket.currency)} · {selectedMarket.market_return.priceDate}</dd></div>}{selectedMarket.currency !== "CNY" && <div><dt>折合人民币</dt><dd>{exchangeRates[selectedMarket.currency] ? money(toCny(selectedMarket, exchangeRates)) : "等待汇率"}</dd></div>}<div><dt>历史/输入年化</dt><dd>{selectedMarket.category === "fixed" ? "不计收益" : needsMarketRate(selectedMarket) && !selectedMarket.market_return ? forecastRateStatus : `${selectedMarket.annual_rate.toFixed(2)}%`}</dd></div>{selectedMarket.code && <div><dt>资产代码</dt><dd>{selectedMarket.code}</dd></div>}{selectedMarket.market_return && <><div><dt>请求历史区间</dt><dd>{selectedMarket.market_return.requestedDays} 天</dd></div><div><dt>实际行情区间</dt><dd>{selectedMarket.market_return.startDate} 至 {selectedMarket.market_return.endDate} · {selectedMarket.market_return.actualDays} 天</dd></div><div><dt>行情缓存日期</dt><dd>{selectedMarket.market_return.calculationDate}{selectedMarket.market_return.stale ? " · 旧数据" : ""}</dd></div></>}<div><dt>稳健年化估计 · 本币</dt><dd>{selectedForecast?`${selectedForecast.annualRate.toFixed(2)}%`:"等待预测数据"}</dd></div><div><dt>{horizon} 年后中位数 · 本币</dt><dd>{selectedForecast?originalMoney(selectedForecast.forecast,selectedMarket.currency):"等待预测数据"}</dd></div></dl>
          <button className="danger-button" onClick={/* 提交当前资产的归档操作，并更新持仓展示。 */ () => removeAsset(selectedMarket)}>归档这项资产</button>
        </aside>
      </div>}

      {toast && <div className="toast" role="status">{toast}</div>}
    </main>
  );
}

const HISTORY_CHART_WIDTH = 680;
const HISTORY_CHART_HEIGHT = 240;
const HISTORY_CHART_PADDING_X = 34;
const HISTORY_CHART_PADDING_Y = 28;

const HistorySection = memo(/* 渲染资产历史表格与走势图，并缓存历史变化和图形坐标的计算结果。 */ function HistorySection({ history }: /* 定义历史图表组件接收的快照列表。 */ { history: HistoryEntry[] }) {
  const { rows, chartHistory, min, max, points, totalChange } = useMemo(/* 缓存历史变化、图表采样点和金额上下界，供表格与走势图复用。 */ () => {
    // Keep the table compact while the chart continues to represent the full
    // retained history.
    const listHistory = history.slice(-7);
    const firstVisibleIndex = Math.max(0, history.length - listHistory.length);
    const rows = listHistory.map(/* 比较相邻快照，计算可见历史记录的金额变化。 */ (entry, index) => {
      const previous = history[firstVisibleIndex + index - 1];
      const change = previous ? entry.total_cny - previous.total_cny : null;
      const changeRate = previous && previous.total_cny ? (change! / previous.total_cny) * 100 : null;
      return { ...entry, change, changeRate };
    });
    const chartPointCount = Math.min(300, history.length);
    const chartHistory = history.length <= chartPointCount ? history : Array.from({ length: chartPointCount }, /* 从完整历史中均匀采样，限制走势图的点数。 */ (_, index) => history[Math.round(index * (history.length - 1) / Math.max(1, chartPointCount - 1))]);
    const values = history.map(/* 提取全部历史资产总额，用于确定图表纵轴范围。 */ (entry) => entry.total_cny);
    const min = values.length ? Math.min(...values) : 0;
    const max = values.length ? Math.max(...values) : 0;
    const points = chartHistory.map(/* 将历史日期索引和资产总额换算为 SVG 图表坐标。 */ (entry, index) => {
      const x = HISTORY_CHART_PADDING_X + index / Math.max(1, chartHistory.length - 1) * (HISTORY_CHART_WIDTH - HISTORY_CHART_PADDING_X * 2);
      const y = HISTORY_CHART_PADDING_Y + (max === min ? 0.5 : (max - entry.total_cny) / (max - min)) * (HISTORY_CHART_HEIGHT - HISTORY_CHART_PADDING_Y * 2);
      return { x, y };
    });
    const latestHistory = history[history.length - 1];
    const totalChange = history.length > 1 ? latestHistory.total_cny - history[0].total_cny : 0;
    return { rows, chartHistory, min, max, points, totalChange };
  }, [history]);

  return <section className="history-section" id="history">
    <div className="section-heading history-heading">
      <div><span className="card-kicker">资产历史</span><h2>总资产走过的轨迹</h2></div>
      <span className="history-count">最近 7 天 · 共 {history.length} 天记录</span>
    </div>
    {history.length === 0 ? <div className="history-empty"><strong>还没有历史记录</strong><span>新增、修改或归档资产后，这里会保存当天最后一次总资产。</span></div> : <>
      {history.length >= 2 ? <div className="history-chart-grid">
        <div className="history-chart" aria-label="历史总资产折线图">
          <div className="history-chart-labels"><span>{money(max)}</span><span>{money(min)}</span></div>
            <svg viewBox={`0 0 ${HISTORY_CHART_WIDTH} ${HISTORY_CHART_HEIGHT}`} role="img" aria-label="按日期排列的历史总资产走势">
              <line x1={HISTORY_CHART_PADDING_X} y1={HISTORY_CHART_PADDING_Y} x2={HISTORY_CHART_WIDTH - HISTORY_CHART_PADDING_X} y2={HISTORY_CHART_PADDING_Y} />
              <line x1={HISTORY_CHART_PADDING_X} y1={HISTORY_CHART_HEIGHT / 2} x2={HISTORY_CHART_WIDTH - HISTORY_CHART_PADDING_X} y2={HISTORY_CHART_HEIGHT / 2} />
              <line x1={HISTORY_CHART_PADDING_X} y1={HISTORY_CHART_HEIGHT - HISTORY_CHART_PADDING_Y} x2={HISTORY_CHART_WIDTH - HISTORY_CHART_PADDING_X} y2={HISTORY_CHART_HEIGHT - HISTORY_CHART_PADDING_Y} />
            <polyline points={points.map(/* 将图表坐标点转换为 SVG 折线需要的坐标文本。 */ (point) => `${point.x},${point.y}`).join(" ")} />
            {points.map(/* 渲染历史曲线的数据点，并附上日期与金额提示。 */ (point, index) => <circle key={chartHistory[index].snapshot_date} cx={point.x} cy={point.y} r="4"><title>{chartHistory[index].snapshot_date} · {money(chartHistory[index].total_cny)}</title></circle>)}
          </svg>
          <div className="history-axis"><span>{history[0].snapshot_date}</span><span>{history[history.length - 1].snapshot_date}</span></div>
        </div>
        <div className="history-summary">
          <span>最新总资产</span><strong>{money(history[history.length - 1].total_cny)}</strong>
          <small>区间变化</small><b className={totalChange < 0 ? "negative" : "positive"}>{totalChange > 0 ? "+" : ""}{money(totalChange)}</b>
        </div>
      </div> : <div className="history-empty compact"><strong>已记录今天的总资产</strong><span>再产生一天记录后显示走势。</span></div>}
      <div className="history-table-wrap">
        <table className="history-table">
          <thead><tr><th>日期</th><th>总资产</th><th>较上次</th><th>变化率</th></tr></thead>
          <tbody>{[...rows].reverse().map(/* 按最新日期优先渲染历史金额及变动记录。 */ (row) => <tr key={row.snapshot_date}>
            <td>{row.snapshot_date}</td><td>{money(row.total_cny)}</td>
            <td className={row.change === null ? "" : row.change < 0 ? "negative" : "positive"}>{row.change === null ? "—" : `${row.change > 0 ? "+" : ""}${money(row.change)}`}</td>
            <td className={row.changeRate === null ? "" : row.changeRate < 0 ? "negative" : "positive"}>{row.changeRate === null ? "—" : `${row.changeRate > 0 ? "+" : ""}${row.changeRate.toFixed(2)}%`}</td>
          </tr>)}</tbody>
        </table>
      </div>
    </>}
  </section>;
});

// AssetForm 渲染新增资产的表单并处理类别关联字段。
function AssetForm({ onSubmit, saving }: /* 定义资产表单的提交回调与保存状态。 */ { onSubmit: (event: FormEvent<HTMLFormElement>) => void; saving: boolean }) {
  const [category, setCategory] = useState<Category>("stock");
  const [currency, setCurrency] = useState<Currency>("CNY");
  const needsCode = ["stock", "fund", "money"].includes(category);
  const quantityAsset = category === "stock" || category === "fund";
  const needsRate = ["deposit", "housing"].includes(category);
  return <form className="asset-form" onSubmit={onSubmit}>
    <label><span>资产类型</span><select name="category" value={category} onChange={/* 更新新增资产类别。 */ (event) => setCategory(event.target.value as Category)}>{(Object.keys(categoryMeta) as Category[]).map(/* 为币种或资产类别渲染下拉选项。 */ (key) => <option value={key} key={key}>{categoryMeta[key].name}</option>)}</select></label>
    <label><span>资产名称</span><input required name="name" placeholder={category === "fixed" ? "例如：自住房产" : "例如：沪深300指数基金"} /></label>
    {needsCode && <label><span>{category === "stock" ? "股票代码" : "基金代码"}</span><input required name="code" inputMode="text" autoCapitalize="characters" placeholder="例如 510300、QQQ、VOO" maxLength={16} onChange={/* 更新资产币种。 */ (event) => {
      const code = event.target.value.trim();
      if (/^[a-z][a-z0-9.-]*$/i.test(code)) setCurrency("USD");
      else if (/^\d{6}$/.test(code)) setCurrency("CNY");
    }} /><small>支持国内 6 位代码和美股代码；QQQ 等美股代码会自动选择美元，也可手动修改</small></label>}
    {quantityAsset
      ? <label><span>持有{category === "stock" ? "股数" : "份数"}</span><input required name="quantity" type="number" min="0.00000001" step="any" placeholder={category === "stock" ? "例如 100.5" : "例如 1250.75"} /><small>支持小数；保存时会读取最新价格并自动计算当前市值和计价币种</small></label>
      : <div className="form-two"><label><span>计价币种</span><select name="currency" value={currency} onChange={/* 更新资产币种。 */ (event) => setCurrency(event.target.value as Currency)}>{(Object.keys(currencyMeta) as Currency[]).map(/* 为币种或资产类别渲染下拉选项。 */ (code) => <option value={code} key={code}>{currencyMeta[code]} · {code}</option>)}</select></label><label><span>当前市值（{currency}）</span><input required name="amount" type="number" min="0.01" step="0.01" placeholder={currency === "CNY" ? "100000" : "10000"} /></label></div>}
    {needsRate && <label><span>年利率（%）</span><input required name="annualRate" type="number" step="0.01" min="0" placeholder="2.60" /></label>}
    <label><span>备注</span><input name="note" placeholder="可选，例如到期日或用途" /></label>
    <button className="primary-button submit" disabled={saving}>{saving ? "正在保存…" : "确认记录"}</button>
  </form>;
}

// LoadingScreen 在数据或登录状态加载时展示占位界面。
function LoadingScreen({ label }: /* 定义加载画面显示的提示文本。 */ { label: string }) {
  return <main className="loading-screen"><div className="loading-mark">复</div><div className="loading-line"><span /></div><p>{label}</p></main>;
}

// AuthScreen 提供登录和注册入口，并将成功后的用户交给父组件。
function AuthScreen({ onAuthenticated }: /* 定义认证成功后向上层传递用户资料的回调。 */ { onAuthenticated: (user: User) => void }) {
  const [mode, setMode] = useState<"login" | "register">("login");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");

  // submit 提交登录或注册表单。
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
          ...(mode === "register" ? { displayName: form.get("displayName") } : {}),
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

  // changeMode 切换登录与注册模式，并重置提示信息。
  function changeMode(next: "login" | "register") {
    setMode(next);
    setError("");
  }

  return <main className="auth-page">
    <section className="auth-story">
      <a className="brand auth-brand" href="#" aria-label="资产星图"><StarMapMark /><span>资产星图</span></a>
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
        <span className="card-kicker">欢迎使用资产星图</span>
        <h2>{mode === "login" ? "登录你的本地账本" : "创建一个本地账户"}</h2>
        <p>{mode === "login" ? "使用本机注册的邮箱与密码继续。" : "账户只在这台设备上有效，不会发送到云端。"}</p>
        <div className="auth-tabs"><button className={mode === "login" ? "active" : ""} onClick={/* 切换登录或注册模式，并由模式切换函数重置提示。 */ () => changeMode("login")}>登录</button><button className={mode === "register" ? "active" : ""} onClick={/* 切换登录或注册模式，并由模式切换函数重置提示。 */ () => changeMode("register")}>注册</button></div>
        <form className="auth-form" onSubmit={submit}>
          {mode === "register" && <label><span>昵称</span><input name="displayName" required minLength={2} maxLength={40} autoComplete="nickname" placeholder="怎么称呼你" /></label>}
          <label><span>邮箱</span><input name="email" required type="email" autoComplete="email" placeholder="name@example.com" /></label>
          <label><span>密码</span><input name="password" required type="password" minLength={8} maxLength={128} autoComplete={mode === "login" ? "current-password" : "new-password"} placeholder={mode === "login" ? "输入密码" : "至少 8 个字符"} /></label>
          {error && <div className="auth-error" role="alert">{error}</div>}
          <button className="primary-button auth-submit" disabled={submitting}>{submitting ? "请稍候…" : mode === "login" ? "登录" : "创建账户"}</button>
        </form>
        <p className="auth-switch">{mode === "login" ? "还没有本地账户？" : "已经有账户？"}<button onClick={/* 切换登录或注册模式，并由模式切换函数重置提示。 */ () => changeMode(mode === "login" ? "register" : "login")}>{mode === "login" ? "立即注册" : "返回登录"}</button></p>
      </div>
    </section>
  </main>;
}

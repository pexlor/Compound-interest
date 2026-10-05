// 统一预测契约：组装情景请求并保护身份与请求顺序，金额统一为分。

// ForecastScenario 保存当前用户本机的通胀、公积金和资产基准选择。
export type ForecastScenario = { inflation: number; includeRestricted: boolean; benchmarks: Record<string, string> };
// ForecastPoint 描述同一模型输出的年度范围、购买力与期末目标概率。
export type ForecastPoint = { year: number; date: string; p10: number; p50: number; p90: number; liquidP50: number; realP50: number; contributions: number; target: number; probability: number };
// ForecastAsset 描述单项资产本币预测和稳健收益估计。
export type ForecastAsset = { id: number; name: string; benchmark: string; currency: string; annualRate: number; months: number; forecast: number };
// ForecastRetirement 是同一情景的退休积累结果，固定资产与未选公积金不计流动余额。
export type ForecastRetirement = { target_cny: number; current_cny: number; liquid_cny: number; complete: boolean; progress: number; projected_years: number | null; projected_date?: string; missing_currencies?: string[]; forecast_state: string; annual_rate: number; items: Array<{ id: number; version: number; name: string; category: string; amount: number; currency: string }> };
// ForecastResponse 描述预测可用性、模型范围及本月收益与本金分项。
export type ForecastResponse = { rates: Record<string,number>; rateDate: string; storedAssets: ForecastStoredAsset[]; state: 'ready' | 'unavailable'; asOf: string; model: string; paths: number; sampleMonths: number; options: ForecastScenario & { years: number }; series: ForecastPoint[]; assets: ForecastAsset[]; warnings: string[]; missing: string[]; retirement: ForecastRetirement; thisMonth: { date: string; investmentGain: number; contributions: number; totalGain: number } };

export const benchmarkLabels: Record<string, string> = { cn_equity: '大陆股票 · 沪深300 ETF', us_equity: '美股 · 标普500 ETF', hk_equity: '港股 · 恒生指数 ETF', cn_bond: '人民币债券 · 国债 ETF' };

// readForecastScenario 解析本机情景，忽略无效或已经不支持的基准配置。
export function readForecastScenario(raw: string | null): ForecastScenario {
  const fallback = { inflation: 2, includeRestricted: false, benchmarks: {} };
  try {
    const value: unknown = JSON.parse(raw ?? 'null');
    if (!value || typeof value !== 'object') return fallback;
    const input = value as Partial<ForecastScenario>;
    const inflation = typeof input.inflation === 'number' && Number.isFinite(input.inflation) && input.inflation >= 0 && input.inflation <= 20 ? input.inflation : 2;
    const benchmarks: Record<string, string> = {};
    if (input.benchmarks && typeof input.benchmarks === 'object') {
      for (const [key, item] of Object.entries(input.benchmarks)) if (/^[1-9]\d*$/.test(key) && typeof item === 'string' && Object.hasOwn(benchmarkLabels, item)) benchmarks[key] = item;
    }
    return { inflation, includeRestricted: input.includeRestricted === true, benchmarks };
  } catch { return fallback; }
}

// forecastURL 构造读取接口，情景假设不受历史年化展示区间影响。
export function forecastURL(years: number, scenario: ForecastScenario): string {
  const query = new URLSearchParams({ years: String(years), inflation: String(scenario.inflation), includeRestricted: String(scenario.includeRestricted), benchmarks: JSON.stringify(scenario.benchmarks) });
  return `/api/forecast?${query.toString()}`;
}

// canApplyForecast 拒绝旧身份或被较新请求替代的迟到预测。
export function canApplyForecast(responseUser: number, activeUser: number | null, responseGeneration: number, activeGeneration: number): boolean {
  return responseUser === activeUser && responseGeneration === activeGeneration;
}

// forecastEnd 仅对有效模拟返回终点，缺数据时不伪造零资产。
export function forecastEnd<T extends { state: string; series: Array<{ year: number; p50: number }> }>(value: T | null): T['series'][number] | null {
  return value?.state === 'ready' && value.series.length ? value.series[value.series.length - 1] : null;
}

// ForecastStoredAsset 是后端一致估值快照，供页面同步已保存余额。
export type ForecastStoredAsset = {id:number;version:number;name:string;category:string;code:string|null;amount:number;quantity:number|null;currency:string;annual_rate:number;note:string;created_at:string};
// sameStoredAssets 判断是否需要同步已保存快照，保持相同余额的引用以避免请求循环。
export function sameStoredAssets(current: Array<{id:number;version:number;amount:number;currency:string;quantity:number|null;annual_rate:number}>, incoming: typeof current): boolean {
 return current.length===incoming.length && current.every((a,i)=>{const b=incoming[i];return a.id===b.id&&a.version===b.version&&a.amount===b.amount&&a.currency===b.currency&&a.quantity===b.quantity&&a.annual_rate===b.annual_rate;});
}

// sameForecastRates 比较同一估值快照的汇率，避免后台汇率更新后总额与预测起点不同。
export function sameForecastRates(current:Record<string,number|undefined>,incoming:Record<string,number>):boolean { return Object.keys(current).length===Object.keys(incoming).length && Object.entries(incoming).every(([key,value])=>current[key]===value); }

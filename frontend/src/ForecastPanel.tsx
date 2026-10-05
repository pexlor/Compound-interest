// 预测面板：展示年度范围、目标概率、情景设置及同口径回测结果。
import { useEffect, useState } from 'react';
import { benchmarkLabels, forecastURL } from './forecast';
import type { ForecastResponse, ForecastScenario } from './forecast';

// PanelAsset 是选择基准所需的当前用户资产信息。
type PanelAsset = { id: number; name: string; category: string; currency: string };
// BacktestSummary 描述回测样本数量、相对误差和区间覆盖率。
type BacktestSummary = { years: number; cases: unknown[]; medianError: number; legacyError: number; coverage: number; note: string };
// money 将最小货币单位格式化为人民币金额。
function money(value: number): string { return new Intl.NumberFormat('zh-CN', { style: 'currency', currency: 'CNY', maximumFractionDigits: 0 }).format(value / 100); }
// defaultBenchmark 展示普通股票自动选择的市场基准，基金默认留空等待明确选择。
function defaultBenchmark(asset: PanelAsset): string { return asset.category === 'stock' ? ({ CNY: 'cn_equity', USD: 'us_equity', HKD: 'hk_equity' }[asset.currency] ?? '') : ''; }

// ForecastPanel 渲染同一后端结果的分位带和概率，不在浏览器重新计算未来收益。
export function ForecastPanel({ data, loading, error, assets, scenario, onChange, years }: { data: ForecastResponse | null; loading: boolean; error: string; assets: PanelAsset[]; scenario: ForecastScenario; onChange: (value: ForecastScenario) => void; years: number }) {
  const [backtests, setBacktests] = useState<BacktestSummary[] | null>(null);
  const [testing, setTesting] = useState(false);
  const [testError, setTestError] = useState('');
  const [run, setRun] = useState({ url: "", count: 0 });
  const url = forecastURL(years, scenario);
  useEffect(/* 情景变化时清空旧回测，用户主动运行才发起计算。 */ () => {
    setBacktests(null); setTestError('');
    if (!run.count || run.url!==url) {setTesting(false);return;}
    const controller = new AbortController(); setTesting(true);
    fetch(`${url}&backtest=1`, { signal: controller.signal })
      .then(/* 校验接口返回后只提取回测指标。 */ async response => { const value = await response.json(); if (!response.ok) throw new Error(value.error || '回测暂不可用'); return value.backtests as BacktestSummary[]; })
      .then(/* 仅在当前情景仍有效时应用指标。 */ result => { if (!controller.signal.aborted) setBacktests(result); })
      .catch(/* 被取消的旧回测不产生提示。 */ reason => { if (!controller.signal.aborted) setTestError(reason instanceof Error ? reason.message : '回测失败'); })
      .finally(/* 清除仍有效回测的加载状态。 */ () => { if (!controller.signal.aborted) setTesting(false); });
    return /* 卸载或变更情景时取消旧回测。 */ () => controller.abort();
  }, [url, run]);
  const ready = data?.state === 'ready' && !loading;
  const series = ready ? data.series : [];
  const end = series[series.length - 1];
  const min = Math.min(...series.map(/* 提取区间下界用于统一图轴。 */ point => point.p10));
  const max = Math.max(...series.map(/* 提取区间上界用于统一图轴。 */ point => point.p90));
  // point 将年度金额映射到同一图轴，横轴从当前到所选期限。
  const point = (index: number, value: number) => `${40 + index / Math.max(1, series.length - 1) * 620},${225 - (value - min) / Math.max(1, max - min) * 185}`;
  const line = series.map(/* 生成中位数折线路径。 */ (item, index) => `${index ? 'L' : 'M'}${point(index, item.p50)}`).join(' ');
  const band = series.map(/* 生成范围上边界。 */ (item, index) => `${index ? 'L' : 'M'}${point(index, item.p90)}`).join(' ') + ' ' + [...series].reverse().map(/* 逆序闭合范围下边界。 */ (item, index) => `L${point(series.length - index - 1, item.p10)}`).join(' ') + ' Z';
  return <div className="robust-forecast">
    <div className="forecast-settings">
      <label>年通胀假设（%）<input aria-label="年通胀假设" type="number" min="0" max="20" step="0.5" value={scenario.inflation} onChange={/* 更新有界通胀情景。 */ event => { const value = Number(event.target.value); if (Number.isFinite(value) && value >= 0 && value <= 20) onChange({ ...scenario, inflation: value }); }} /></label>
    </div>
    {assets.some(/* 检查是否存在需要市场基准的持仓。 */ asset => ['stock', 'fund'].includes(asset.category)) && <details className="benchmark-settings" open={assets.some(/* 未配置的基金需要显式选择，默认展开入口。 */ asset => asset.category === 'fund' && !scenario.benchmarks[asset.id])}>
      <summary>资产预测基准</summary><p>按资产实际投资市场选择；债券基金请选择债券基准。历史不足三年时采用基准收益与波动。</p>
      {assets.filter(/* 只展示风险资产的基准选择。 */ asset => ['stock', 'fund'].includes(asset.category)).map(/* 按资产选择明确基准，不推断普通基金类型。 */ asset => <label key={asset.id}><span>{asset.name}</span><select aria-label={`${asset.name}预测基准`} value={scenario.benchmarks[asset.id] ?? defaultBenchmark(asset)} onChange={/* 仅更新当前资产的基准选择。 */ event => { const benchmarks = { ...scenario.benchmarks }; if (event.target.value) benchmarks[asset.id] = event.target.value; else delete benchmarks[asset.id]; onChange({ ...scenario, benchmarks }); }}><option value="">请选择投资市场</option>{Object.entries(benchmarkLabels).map(/* 渲染可解释的基准选项。 */ ([key, text]) => <option key={key} value={key}>{text}</option>)}</select></label>)}
    </details>}
    {ready && end ? <>
      <div className="forecast-metrics"><div><span>模型情景范围 · P10—P90</span><strong>{money(end.p10)} ～ {money(end.p90)}</strong></div><div><span>按今日购买力 · 中位数</span><strong>{money(end.realP50)}</strong></div><div><span>计入退休目标 · 中位数</span><strong>{money(end.liquidP50)}</strong></div>{data.retirement.target_cny > 0 && <div><span>{years}年末达到通胀后目标的概率</span><strong>{(end.probability * 100).toFixed(1)}%</strong></div>}</div>
      <div className="forecast-curve"><div className="chart-top"><span>资产增长情景</span><span>中位数与 P10—P90 范围</span></div><svg viewBox="0 0 700 270" role="img" aria-label={`未来${years}年资产中位数及模型情景范围`}><text x="40" y="22">{money(max)}</text><text x="40" y="248">{money(min)}</text><path d={band} fill="#6575d9" opacity="0.16" /><path d={line} fill="none" stroke="#6575d9" strokeWidth="3" /><text x="40" y="266">现在</text><text x="625" y="266">{years}年后</text></svg></div>
      <details className="forecast-table"><summary>查看逐年金额与概率</summary><div className="forecast-table-scroll"><table><thead><tr><th>年份</th><th>P10</th><th>中位数</th><th>P90</th><th>期末达标概率</th></tr></thead><tbody>{series.map(/* 用同一后端结果呈现年度明细。 */ row => <tr key={row.year}><td>{row.year ? `${row.year}年` : '现在'}</td><td>{money(row.p10)}</td><td>{money(row.p50)}</td><td>{money(row.p90)}</td><td>{data.retirement.target_cny > 0 ? `${(row.probability * 100).toFixed(1)}%` : '未设目标'}</td></tr>)}</tbody></table></div></details>
    </> : <div className="unavailable-chart" role="status">{loading ? '正在计算预测情景…' : error || data?.missing.join('；') || '等待预测数据'}</div>}
    {data?.warnings.length ? <details className="forecast-warnings"><summary>数据与计算假设（{data.warnings.length}项）</summary><ul>{data.warnings.map(/* 展示影响结果解释的数据与模型限制。 */ (warning, index) => <li key={index}>{warning}</li>)}</ul></details> : null}
    <p className="disclaimer">现有资产分别计算收益，新增储蓄、奖金和期权只计本金。全部已记录资产均计入退休目标；固定资产按当前估值保持不变。通胀为情景假设；达标只表示积累到目标金额，不表示退休后支出可持续。模拟范围不能覆盖所有未来风险。</p>
    <div className="backtest-control"><button type="button" disabled={testing || loading} onClick={/* 用户主动请求滚动回测。 */ () => setRun(value => ({url,count:value.count+1}))}>{testing ? '正在回测…' : '运行历史回测'}</button><small>仅用历史时点之前的数据，比较1、3、5年预测</small></div>
    {testError && <p role="status">{testError}</p>}
    {backtests && <div className="backtest-results">{backtests.map(/* 明确样本数量，样本不足时不显示伪造误差百分比。 */ report => <p key={report.years}><b>{report.years}年回测 · {report.cases.length}个窗口</b>{report.cases.length ? <span> 新算法平均误差 {(report.medianError * 100).toFixed(1)}% · 原算法 {(report.legacyError * 100).toFixed(1)}% · 范围覆盖率 {(report.coverage * 100).toFixed(1)}%</span> : <span> 缓存历史不足，暂无法评估。</span>}<small>{report.note}</small></p>)}</div>}
  </div>;
}

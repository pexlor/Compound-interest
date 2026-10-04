// 收入计划编辑界面：维护工资、年终奖、期权授予、归属与变现安排。

import { useState, type FormEvent } from 'react';
import { generateBatches, nextBonus, type IncomeSettings, type IncomeInput, type OptionGrant, type OptionBatch } from './income';

let nextGrantKey = 0;

// amountText 将主货币单位金额格式化为指定币种的中文金额文本。
const amountText = (n: number, currency = 'CNY') => new Intl.NumberFormat('zh-CN', { style: 'currency', currency, maximumFractionDigits: 2 }).format(n);

// GrantEditor 编辑单份期权授予的数量、价格、税率以及逐批归属和变现安排。
function GrantEditor({ grant, onChange, onRemove }: /* 定义单份授予编辑器的数据、修改回调和删除回调。 */ { grant: OptionGrant; onChange: (g: OptionGrant) => void; onRemove: () => void }) {
  const [firstDate, setFirstDate] = useState(grant.batches[0]?.vestDate ?? '');
  const [periods, setPeriods] = useState(4);
  const [interval, setInterval] = useState(12);
  const [percent, setPercent] = useState(25);
  // patch 将修改字段合并到当前期权授予，并通知上层保存草稿。
  const patch = (values: Partial<OptionGrant>) => onChange({ ...grant, ...values });
  // batch 更新指定归属批次的字段，同时保留其余批次。
  const batch = (index: number, values: Partial<OptionBatch>) => patch({ batches: grant.batches.map(/* 仅合并指定索引的修改，保留其他授予或批次。 */ (b, i) => i === index ? { ...b, ...values } : b) });
  const assigned = grant.batches.reduce(/* 累计归属批次数量，用于校验授予总量。 */ (sum, b) => sum + b.quantity, 0);
  const netPerShare = Math.max(0, grant.marketPrice - grant.strikePrice) * (1 - grant.taxRate / 100);
  const held = grant.batches.filter(/* 筛选暂不变现的期权归属批次。 */ b => b.cashMode === 'hold').reduce(/* 累计归属批次数量，用于校验授予总量。 */ (sum, b) => sum + b.quantity, 0);
  return <fieldset className="option-grant">
    <legend>{grant.name || '新期权授予'}</legend>
    <div className="planner-grid">
      <label>授予名称<input required value={grant.name} maxLength={120} onChange={/* 更新期权草稿中的名称。 */ e => patch({ name: e.target.value })} placeholder="例如：2026 年入职期权" /></label>
      <label>币种<select value={grant.currency} onChange={/* 更新期权草稿中的币种。 */ e => patch({ currency: e.target.value })}>{['CNY','USD','HKD','EUR','JPY','GBP','SGD','AUD','CAD','CHF'].map(/* 为币种或资产类别渲染下拉选项。 */ c => <option key={c}>{c}</option>)}</select></label>
      <label>授予总数量<input required type="number" min="0.000001" max="1000000000" step="any" value={grant.quantity} onChange={/* 更新期权草稿中的数量。 */ e => patch({ quantity: Number(e.target.value) })} /></label>
      <label>每份行权价<input required type="number" min="0" max="1000000000" step="any" value={grant.strikePrice} onChange={/* 更新期权草稿中的行权价。 */ e => patch({ strikePrice: Number(e.target.value) })} /></label>
      <label>每份预估股价<input required type="number" min="0" max="1000000000" step="any" value={grant.marketPrice} onChange={/* 更新期权草稿中的预估股价。 */ e => patch({ marketPrice: Number(e.target.value) })} /></label>
      <label>预估税率（%）<input required type="number" min="0" max="100" step="any" value={grant.taxRate} onChange={/* 更新期权草稿中的预估税率。 */ e => patch({ taxRate: Number(e.target.value) })} /></label>
    </div>
    <p className="planner-help">净收入 = 数量 × max（预估股价 − 行权价，0）×（1 − 税率）。价格和税率用于情景预测，金额以所选币种填写。</p>
    <details className="schedule-generator">
      <summary>生成分期归属计划</summary>
      <div className="planner-grid">
        <label>首次归属日期<input type="date" value={firstDate} onChange={/* 更新首次归属日期。 */ e => setFirstDate(e.target.value)} /></label>
        <label>总期数<input type="number" min="1" max="1200" value={periods} onChange={/* 更新归属期数。 */ e => setPeriods(Number(e.target.value))} /></label>
        <label>归属间隔<select value={interval} onChange={/* 更新归属间隔月数。 */ e => setInterval(Number(e.target.value))}><option value={12}>每年</option><option value={3}>每季度</option><option value={1}>每月</option></select></label>
        <label>首期归属比例（%）<input type="number" min="0.000001" max="100" step="any" value={percent} onChange={/* 更新首期归属比例。 */ e => setPercent(Number(e.target.value))} /></label>
      </div>
      <button type="button" disabled={!firstDate || !Number.isInteger(periods) || periods < 1 || periods > 1200 || grant.quantity <= 0 || (periods > 1 && (percent <= 0 || percent >= 100))} onClick={/* 按归属生成器参数替换当前授予的全部归属批次。 */ () => patch({ batches: generateBatches(grant.quantity, firstDate, periods, interval, percent) })}>生成并替换本份归属明细</button>
      <p className="planner-help">首期可设置集中归属比例，剩余数量均分到后续各期。生成后仍可逐笔调整。</p>
    </details>
    <div className="option-batches">{grant.batches.map(/* 更新期权草稿中的归属日期。 */ (b, index) => <div className="option-batch" key={index}>
      <label>归属日期<input required type="date" value={b.vestDate} onChange={/* 更新期权草稿中的归属日期。 */ e => batch(index, { vestDate: e.target.value })} /></label>
      <label>归属数量<input required type="number" min="0.000001" step="any" value={b.quantity} onChange={/* 更新期权草稿中的数量。 */ e => batch(index, { quantity: Number(e.target.value) })} /></label>
      <label>变现安排<select value={b.cashMode} onChange={/* 更新期权草稿中的变现方式。 */ e => batch(index, { cashMode: e.target.value as OptionBatch['cashMode'] })}><option value="immediate">归属即变现</option><option value="date">指定变现日期</option><option value="hold">暂不变现</option></select></label>
      {b.cashMode === 'date' && <label>变现日期<input required type="date" min={b.vestDate} value={b.cashDate} onChange={/* 更新期权草稿中的变现日期。 */ e => batch(index, { cashDate: e.target.value })} /></label>}
      <span className="batch-net">预计净额 {amountText(b.quantity * netPerShare, grant.currency)}</span>
      <button type="button" className="planner-remove" onClick={/* 移除指定的期权归属批次。 */ () => patch({ batches: grant.batches.filter(/* 排除指定索引的授予或归属批次。 */ (_, i) => i !== index) })}>删除此期</button>
    </div>)}</div>
    <p className={assigned > grant.quantity + 1e-6 ? 'planner-error' : 'planner-help'}>已安排 {Number(assigned.toFixed(6))} / {grant.quantity} 份 · 未安排 {Number((grant.quantity - assigned).toFixed(6))} 份 · 暂不变现 {held} 份</p>
    <div className="planner-actions"><button type="button" onClick={/* 为当前期权授予追加一个待编辑的归属批次。 */ () => patch({ batches: [...grant.batches, { vestDate: '', quantity: Math.max(0, grant.quantity - assigned), cashMode: 'immediate', cashDate: '' }] })}>添加归属批次</button><button type="button" className="planner-remove" onClick={onRemove}>删除本份授予</button></div>
  </fieldset>;
}

// IncomePlanner 维护工资、储蓄、奖金与期权草稿，校验归属数量后提交收入计划。
export function IncomePlanner({ income, saving, onSave }: /* 定义收入编辑器的数据、保存状态和异步保存回调。 */ { income: IncomeSettings; saving: boolean; onSave: (input: IncomeInput) => Promise<void> }) {
  const [salary, setSalary] = useState(income.monthly_salary / 100);
  const [savings, setSavings] = useState(income.monthly_savings / 100);
  const [bonus, setBonus] = useState(income.annual_bonus / 100);
  const [settings, setSettings] = useState(income.bonus_settings ?? { workStartDate: '', payMonth: 2, payDay: 28, yearOffset: 1 });
  const [grants, setGrants] = useState<Array<OptionGrant & /* 为期权草稿补充稳定的前端编辑标识。 */ { draftKey: number }>>(/* 初始化期权草稿，并为每份授予生成稳定的编辑标识。 */ () => income.options.map(/* 复制已保存授予，并分配稳定的前端编辑标识。 */ g => ({ ...g, draftKey: ++nextGrantKey })));
  const today = income.forecast_as_of;
  const preview = settings.workStartDate ? nextBonus(bonus, settings, today) : null;
  const [error, setError] = useState('');
  // submit 校验归属总数量，并将主货币单位的收入设置和期权草稿提交保存。
  const submit = async (event: FormEvent) => {
    event.preventDefault(); setError('');
    if (grants.some(/* 检查是否有授予安排了超过总数量的归属批次。 */ g => g.batches.reduce(/* 累计归属批次数量，用于校验授予总量。 */ (sum, b) => sum + b.quantity, 0) > g.quantity + 1e-6)) { setError('归属数量不能超过授予总数量'); return; }
    await onSave({ version: income.version, monthlySalary: salary, monthlySavings: savings, annualBonus: bonus, bonusSettings: settings.workStartDate ? settings : undefined, options: grants.map(/* 移除仅供前端编辑使用的标识，生成提交给后端的授予数据。 */ ({ draftKey: _draftKey, ...grant }) => grant) });
  };
  const upcoming = income.cashflows.slice(0, 8);
  return <form className="income-form income-planner" onSubmit={submit}>
    <h3>工资、年终奖与期权</h3>
    <div className="planner-grid">
      <label>当前月工资（人民币）<input required type="number" min="0" step="0.01" value={salary} onChange={/* 更新月工资草稿。 */ e => setSalary(Number(e.target.value))} /></label>
      <label>每月预计储蓄额<input required type="number" min="0" step="0.01" value={savings} onChange={/* 更新每月储蓄草稿。 */ e => setSavings(Number(e.target.value))} /></label>
    </div>
    <fieldset><legend>年终奖</legend><div className="planner-grid">
      <label>完整年终奖（税前人民币）<input required type="number" min="0" step="0.01" value={bonus} onChange={/* 更新年终奖金额草稿。 */ e => setBonus(Number(e.target.value))} /></label>
      <label>开始工作日期<input required={bonus > 0} type="date" value={settings.workStartDate} onChange={/* 更新奖金计划的入职日期。 */ e => setSettings({ ...settings, workStartDate: e.target.value })} /></label>
      <label>领取月<input required type="number" min="1" max="12" value={settings.payMonth} onChange={/* 更新奖金计划的领取月份。 */ e => setSettings({ ...settings, payMonth: Number(e.target.value) })} /></label>
      <label>领取日<input required type="number" min="1" max={new Date(Date.UTC(2000, settings.payMonth, 0)).getUTCDate()} value={settings.payDay} onChange={/* 更新奖金计划的领取日。 */ e => setSettings({ ...settings, payDay: Number(e.target.value) })} /></label>
      <label>奖金所属年度<select value={settings.yearOffset} onChange={/* 更新奖金计划的奖金所属年度。 */ e => setSettings({ ...settings, yearOffset: Number(e.target.value) })}><option value={1}>领取上一年度奖金</option><option value={0}>领取当年度奖金</option></select></label>
    </div>
    <p className="planner-help">到手年终奖 = 所属年度在职天数 ÷ 当年总天数 × 完整金额 × 90%。入职日计入在职天数，未来按持续在职估算；2 月 29 日在平年按 2 月 28 日领取。金额为 0 时停用。</p>
    {preview && <p className="bonus-preview">下次领取 {preview.date} · {preview.earningYear} 年度 · 工作比例 {(preview.ratio * 100).toFixed(2)}% · 预计到手 <b>{amountText(preview.amount / 100)}</b></p>}
    </fieldset>
    <h4>期权授予与归属</h4>
    {grants.length === 0 && <p className="planner-help">添加每份授予，分别安排多年归属及变现日期。</p>}
    {grants.map(/* 删除指定的期权授予草稿。 */ (grant, index) => <GrantEditor key={grant.draftKey} grant={grant} onChange={/* 保存指定授予的修改，同时保留其稳定编辑标识。 */ g => setGrants(grants.map(/* 仅合并指定索引的修改，保留其他授予或批次。 */ (item, i) => i === index ? { ...g, draftKey: item.draftKey } : item))} onRemove={/* 删除指定的期权授予草稿。 */ () => setGrants(grants.filter(/* 排除指定索引的授予或归属批次。 */ (_, i) => i !== index))} />)}
    <button type="button" className="add-option" onClick={/* 追加一份带默认数量和币种的期权授予草稿。 */ () => setGrants([...grants, { draftKey: ++nextGrantKey, name: '', currency: 'CNY', quantity: 100, strikePrice: 0, marketPrice: 0, taxRate: 0, batches: [] }])}>添加期权授予</button>
    <p className="planner-help">已保存计划中未来到账的年终奖和期权净额计入储蓄并参与收益预测。暂不变现及未安排归属的期权单独保留；截至今天已到账的收入由当前资产体现，请在资产中维护实际余额。</p>
    {upcoming.length > 0 && <details className="upcoming-income"><summary>已保存计划 · 后续到账预览</summary><ul>{upcoming.map(/* 渲染未来现金流的到账日期、来源与净额。 */ (e, i) => <li key={i}><span>{e.date} · {e.name}{e.earning_year ? `（${e.earning_year}年度）` : ''}</span><b>{amountText(e.amount / 100, e.currency)}</b></li>)}</ul></details>}
    <small className="annual-income">工资年收入 {amountText(salary * 12)} · 完整年度税后奖金 {amountText(bonus * .9)}</small>
    {error && <p role="alert" className="planner-error">{error}</p>}
    <button type="submit" disabled={saving}>{saving ? '保存中…' : '保存收入与归属计划'}</button>
  </form>;
}

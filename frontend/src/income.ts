// 收入计划数据模型与计算工具：生成期权归属批次并预览下次年终奖。

// BonusSettings 描述入职日期、奖金领取月日和奖金所属年度偏移。
export type BonusSettings = { workStartDate: string; payMonth: number; payDay: number; yearOffset: number };
// OptionBatch 描述一批期权的归属日期、数量和变现方式。
export type OptionBatch = { vestDate: string; quantity: number; cashMode: 'immediate' | 'date' | 'hold'; cashDate: string };
// OptionGrant 描述一份期权授予的币种、价格、税率与归属批次。
export type OptionGrant = { name: string; currency: string; quantity: number; strikePrice: number; marketPrice: number; taxRate: number; batches: OptionBatch[] };
// Cashflow 表示指定日期到账的收入，金额以最小货币单位记录。
export type Cashflow = { date: string; amount: number; currency: string; kind?: string; name?: string; work_ratio?: number; earning_year?: number };
// IncomeSettings 对应后端收入响应，金额使用最小货币单位并包含预测现金流。
export type IncomeSettings = { version: number; monthly_salary: number; monthly_savings: number; annual_bonus: number; updated_at: string | null; bonus_settings: BonusSettings | null; options: OptionGrant[]; cashflows: Cashflow[]; forecast_as_of: string };
// IncomeInput 对应收入计划保存请求，工资、储蓄与奖金以主货币单位提交。
export type IncomeInput = { version: number; monthlySalary: number; monthlySavings: number; annualBonus: number; bonusSettings?: BonusSettings; options: OptionGrant[] };

// Generate regular installments after an optional first cliff. Clamp month ends
// instead of JavaScript's default date rollover (Jan 31 -> March).
// generateBatches 按首期比例生成后续归属批次，将月末日期限制在目标月份内并保留授予总数量。
export function generateBatches(quantity: number, firstDate: string, periods: number, intervalMonths: number, firstPercent: number): OptionBatch[] {
  if (!firstDate || quantity <= 0 || periods < 1 || periods > 1200 || firstPercent < 0 || firstPercent > 100) return [];
  const start = new Date(`${firstDate}T00:00:00Z`);
  if (!Number.isFinite(start.getTime())) return [];
  const first = periods === 1 ? quantity : quantity * firstPercent / 100;
  if (periods > 1 && (first <= 0 || first >= quantity)) return [];
  let assigned = 0;
  return Array.from({ length: periods }, /* 计算当前批次的月末截断日期和数量，最后一批补齐舍入余量。 */ (_, index) => {
    const month = new Date(Date.UTC(start.getUTCFullYear(), start.getUTCMonth() + index * intervalMonths, 1));
    const lastDay = new Date(Date.UTC(month.getUTCFullYear(), month.getUTCMonth() + 1, 0)).getUTCDate();
    month.setUTCDate(Math.min(start.getUTCDate(), lastDay));
    const part = index === periods - 1 ? quantity - assigned : Number((index === 0 ? first : (quantity - first) / (periods - 1)).toFixed(6));
    assigned += part;
    return { vestDate: month.toISOString().slice(0, 10), quantity: Number(part.toFixed(6)), cashMode: 'immediate', cashDate: '' };
  });
}

// nextBonus 根据入职日、领取月日和奖金所属年度，计算下次领取日期与九折税后金额。
export function nextBonus(bonus: number, settings: BonusSettings, today: string) {
  const start = new Date(`${settings.workStartDate}T00:00:00Z`);
  if (!Number.isFinite(start.getTime())) return null;
  let year = Number(today.slice(0, 4));
  // dateFor 计算指定年份的奖金领取日，遇到不存在的月末日期时取该月最后一天。
  const dateFor = (y: number) => {
    const last = new Date(Date.UTC(y, settings.payMonth, 0)).getUTCDate();
    return new Date(Date.UTC(y, settings.payMonth - 1, Math.min(last, settings.payDay)));
  };
  let pay = dateFor(year);
  if (pay.toISOString().slice(0, 10) <= today) pay = dateFor(++year);
  const earningYear = year - settings.yearOffset;
  const a = Date.UTC(earningYear, 0, 1), z = Date.UTC(earningYear + 1, 0, 1);
  const ratio = Math.max(0, (z - Math.max(a, start.getTime())) / (z - a));
  return { date: pay.toISOString().slice(0, 10), ratio, amount: Math.round(bonus * 100 * ratio * .9), earningYear };
}

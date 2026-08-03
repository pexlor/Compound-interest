type FundPoint = { FSRQ: string; DWJZ: string; LJJZ: string };

function domesticStockSymbol(code: string) {
  if (/^(5|6|9)/.test(code)) return `sh${code}`;
  if (/^(0|1|2|3)/.test(code)) return `sz${code}`;
  return code;
}

function isUsSecurityCode(code: string) {
  return /^[A-Z][A-Z0-9.-]{0,14}$/.test(code);
}

async function resolveStockSymbol(code: string) {
  if (!isUsSecurityCode(code)) return domesticStockSymbol(code);

  const lookupSymbol = `us${code}`;
  const lookupUrl = `https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?param=${encodeURIComponent(lookupSymbol)},day,,,2,qfq`;
  const response = await fetch(lookupUrl, { headers: { "User-Agent": "Mozilla/5.0" } });
  if (!response.ok) throw new Error("行情服务暂时不可用");
  const json = (await response.json()) as {
    data?: Record<string, { qt?: Record<string, (string | number)[]> }>;
  };
  const resolvedCode = String(json.data?.[lookupSymbol]?.qt?.[lookupSymbol]?.[2] ?? "").trim();
  return resolvedCode ? `us${resolvedCode}` : lookupSymbol;
}

function annualize(start: number, end: number, days: number) {
  if (start <= 0 || end <= 0 || days <= 0) return 0;
  return (Math.pow(end / start, 365 / days) - 1) * 100;
}

async function stockReturn(code: string, days: number) {
  const count = Math.min(1250, Math.max(30, Math.ceil(days * 0.72)));
  const isUsSecurity = isUsSecurityCode(code);
  const symbol = await resolveStockSymbol(code);
  const url = `https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?param=${encodeURIComponent(symbol)},day,,,${count},qfq`;
  const response = await fetch(url, { headers: { "User-Agent": "Mozilla/5.0" } });
  if (!response.ok) throw new Error("行情服务暂时不可用");
  const json = (await response.json()) as {
    data?: Record<string, { qfqday?: (string | object)[][]; day?: (string | object)[][] }>;
  };
  const rows = json.data?.[symbol]?.qfqday ?? json.data?.[symbol]?.day ?? [];
  if (rows.length < 2) throw new Error("没有找到这个股票代码的历史行情");
  const first = rows[0];
  const last = rows[rows.length - 1];
  const start = Number(first[2]);
  const end = Number(last[2]);
  const actualDays = Math.max(1, (Date.parse(String(last[0])) - Date.parse(String(first[0]))) / 86400000);
  return {
    annualRate: annualize(start, end, actualDays),
    periodReturn: (end / start - 1) * 100,
    startDate: first[0],
    endDate: last[0],
    source: isUsSecurity ? "腾讯证券美股历史行情" : "腾讯证券历史复权行情",
  };
}

async function fundReturn(code: string, days: number, isMoney: boolean) {
  const desiredPoints = isMoney ? 20 : Math.min(1500, Math.max(20, Math.ceil(days * 0.75)));
  const pageSize = 20;
  const pageCount = Math.min(40, Math.ceil(desiredPoints / pageSize));
  const pages = await Promise.all(Array.from({ length: pageCount }, async (_, index) => {
    const url = `https://api.fund.eastmoney.com/f10/lsjz?fundCode=${encodeURIComponent(code)}&pageIndex=${index + 1}&pageSize=${pageSize}`;
    const response = await fetch(url, {
      headers: { "User-Agent": "Mozilla/5.0", Referer: "https://fundf10.eastmoney.com/" },
    });
    if (!response.ok) throw new Error("基金数据服务暂时不可用");
    return (await response.json()) as { Data?: { LSJZList?: FundPoint[]; SYType?: string } };
  }));
  const points = pages.flatMap((page) => page.Data?.LSJZList ?? []);
  const yieldType = pages[0]?.Data?.SYType;
  if (points.length < 2) throw new Error("没有找到这个基金代码的数据");

  if (isMoney || yieldType?.includes("每万份收益")) {
    const sample = points.slice(0, 7);
    const averageDaily = sample.reduce((sum, point) => sum + (Number(point.DWJZ) || 0), 0) / sample.length;
    const annualRate = averageDaily * 3.65;
    return {
      annualRate,
      periodReturn: annualRate,
      startDate: sample[sample.length - 1].FSRQ,
      endDate: sample[0].FSRQ,
      source: "东方财富每万份收益折算",
    };
  }

  const latest = points[0];
  const oldest = points[points.length - 1];
  const start = Number(oldest.LJJZ) || Number(oldest.DWJZ);
  const end = Number(latest.LJJZ) || Number(latest.DWJZ);
  const actualDays = Math.max(1, (Date.parse(latest.FSRQ) - Date.parse(oldest.FSRQ)) / 86400000);
  return {
    annualRate: annualize(start, end, actualDays),
    periodReturn: (end / start - 1) * 100,
    startDate: oldest.FSRQ,
    endDate: latest.FSRQ,
    source: "东方财富历史净值",
  };
}

export async function GET(request: Request) {
  const params = new URL(request.url).searchParams;
  const rawCode = params.get("code")?.trim() ?? "";
  const code = /^[a-z]/i.test(rawCode) ? rawCode.toUpperCase() : rawCode;
  const category = params.get("category") ?? "stock";
  const days = Math.min(1825, Math.max(30, Number(params.get("days")) || 365));
  if (!code) return Response.json({ error: "请输入代码" }, { status: 400 });
  try {
    const result = category === "stock" || (category === "fund" && (/^[15]/.test(code) || isUsSecurityCode(code)))
      ? await stockReturn(code, days)
      : await fundReturn(code, days, category === "money");
    return Response.json(result);
  } catch (error) {
    return Response.json(
      { error: error instanceof Error ? error.message : "读取行情失败" },
      { status: 502 }
    );
  }
}

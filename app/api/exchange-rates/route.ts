import { getAuthenticatedUser } from "../../../db/auth";

const foreignCurrencies = ["USD", "HKD", "EUR", "JPY", "GBP", "SGD", "AUD", "CAD", "CHF"] as const;
const CACHE_TIME_MS = 15 * 60 * 1000;

type RateRow = {
  date: string;
  base: string;
  quote: string;
  rate: number;
};

type RatesPayload = {
  rates: Record<string, number>;
  date: string;
  source: string;
  fetchedAt: string;
};

let cache: (RatesPayload & { expiresAt: number }) | null = null;

function unauthorized() {
  return Response.json({ error: "请先登录" }, { status: 401 });
}

async function fetchLatestRates(): Promise<RatesPayload> {
  const endpoint = new URL("https://api.frankfurter.dev/v2/rates");
  endpoint.searchParams.set("base", "CNY");
  endpoint.searchParams.set("quotes", foreignCurrencies.join(","));
  const response = await fetch(endpoint, { headers: { Accept: "application/json" } });
  if (!response.ok) throw new Error("最新汇率服务暂不可用");

  const rows = await response.json() as RateRow[];
  const rates: Record<string, number> = { CNY: 1 };
  for (const currency of foreignCurrencies) {
    const row = rows.find((item) => item.quote === currency);
    if (!row || !Number.isFinite(row.rate) || row.rate <= 0) {
      throw new Error(`未取得 ${currency} 的最新汇率`);
    }
    rates[currency] = 1 / row.rate;
  }

  return {
    rates,
    date: rows.map((row) => row.date).sort().at(-1) || "",
    source: "Frankfurter 央行参考汇率",
    fetchedAt: new Date().toISOString(),
  };
}

export async function GET(request: Request) {
  const user = await getAuthenticatedUser(request);
  if (!user) return unauthorized();

  const force = new URL(request.url).searchParams.get("refresh") === "1";
  if (!force && cache && cache.expiresAt > Date.now()) {
    return Response.json({ ...cache, cached: true });
  }

  try {
    const payload = await fetchLatestRates();
    cache = { ...payload, expiresAt: Date.now() + CACHE_TIME_MS };
    return Response.json({ ...payload, cached: false });
  } catch (error) {
    if (cache) {
      return Response.json({ ...cache, cached: true, stale: true });
    }
    return Response.json(
      { error: error instanceof Error ? error.message : "读取最新汇率失败" },
      { status: 502 }
    );
  }
}

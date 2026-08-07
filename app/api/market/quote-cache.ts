export type CachedQuote = {
  currentPrice: number;
  priceCurrency: "CNY" | "USD";
  priceDate: string;
};

export type QuoteCache = {
  values: Map<string, { value: CachedQuote; expiresAt: number }>;
  inFlight: Map<string, Promise<CachedQuote>>;
  ttlMs: number;
};

export function createQuoteCache(ttlMs = 60_000): QuoteCache {
  return { values: new Map(), inFlight: new Map(), ttlMs };
}

export const sharedQuoteCache = createQuoteCache();

export async function readCachedQuote(
  cache: QuoteCache,
  key: string,
  load: () => Promise<CachedQuote>,
  now = Date.now,
) {
  const timestamp = now();
  const cached = cache.values.get(key);
  if (cached && cached.expiresAt > timestamp) return cached.value;

  let pending = cache.inFlight.get(key);
  if (!pending) {
    pending = load();
    cache.inFlight.set(key, pending);
  }
  try {
    const value = await pending;
    cache.values.set(key, { value, expiresAt: now() + cache.ttlMs });
    return value;
  } finally {
    if (cache.inFlight.get(key) === pending) cache.inFlight.delete(key);
  }
}

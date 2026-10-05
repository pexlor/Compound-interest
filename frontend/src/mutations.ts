// mutationHeaders 为 JSON 写请求生成内容类型和唯一幂等键。
export function mutationHeaders(source: Pick<Crypto, "getRandomValues"> & Partial<Pick<Crypto, "randomUUID">> = globalThis.crypto) {
  // randomUUID 只在安全上下文可用，内网 HTTP 使用同样的128位随机数生成编号。
  const key = typeof source.randomUUID === "function"
    ? source.randomUUID()
    : Array.from(source.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, "0")).join("");
  return { "Content-Type": "application/json", "Idempotency-Key": key };
}

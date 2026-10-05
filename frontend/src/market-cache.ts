// CachedMarketRate 描述缓存年化的区间、有效性和同步状态。
export type CachedMarketRate = {
 category?: string;
 code?: string;
 annualRate: number;
 annualReady?: boolean;
 requestedDays: number;
 pending?: boolean;
 stale?: boolean;
};
// marketCacheKey 生成界面行情映射键，统一代码大小写。
export const marketCacheKey=(category:string,code:string)=>`${category}:${code.trim().toUpperCase()}`;
// mergeMarketResults 仅合并当前区间的有效年化，保留补齐中的旧成功结果。
export function mergeMarketResults<T extends CachedMarketRate>(previous:Record<string,T>,results:T[],days:number):Record<string,T>{
 const next:Record<string,T>={};
 for(const [key,value] of Object.entries(previous)){
  if(value.requestedDays===days && value.annualReady===true && Number.isFinite(value.annualRate))next[key]=value;
 }
 for(const result of results){
  if(result.category && result.code && result.requestedDays===days && result.annualReady===true && Number.isFinite(result.annualRate))next[marketCacheKey(result.category,result.code)]=result;
 }
 return next;
}
// needsMarketRate 判断持仓是否依赖支持的数据源提供历史年化。
export function needsMarketRate(asset:{category:string;code:string|null}){
 return Boolean(asset.code && (asset.category==='stock'||asset.category==='fund'||(asset.category==='money'&&/^\d{6}$/.test(asset.code))));
}
// missingMarketRates 找出当前区间尚无有效年化的持仓，避免按零利率推演。
export function missingMarketRates(assets:Array<{category:string;code:string|null}>,rates:Record<string,CachedMarketRate>,days:number){
 return assets.filter(asset=>{
  if(!needsMarketRate(asset))return false;
  const value=rates[marketCacheKey(asset.category,asset.code!)];
  return !value||value.annualReady!==true||value.requestedDays!==days||!Number.isFinite(value.annualRate);
 });
}

// shouldApplyMarketResponse 拒绝已切换区间或被较新请求取代的迟到响应。
export function shouldApplyMarketResponse(responseDays:number,activeDays:number,responseGeneration:number,currentGeneration:number){
 return responseDays===activeDays && responseGeneration===currentGeneration;
}

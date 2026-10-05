export type CachedMarketRate = {
 category?: string;
 code?: string;
 annualRate: number;
 annualReady?: boolean;
 requestedDays: number;
 pending?: boolean;
 stale?: boolean;
};
export const marketCacheKey=(category:string,code:string)=>`${category}:${code.trim().toUpperCase()}`;
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
export function needsMarketRate(asset:{category:string;code:string|null}){
 return Boolean(asset.code && (asset.category==='stock'||asset.category==='fund'||(asset.category==='money'&&/^\d{6}$/.test(asset.code))));
}
export function missingMarketRates(assets:Array<{category:string;code:string|null}>,rates:Record<string,CachedMarketRate>,days:number){
 return assets.filter(asset=>{
  if(!needsMarketRate(asset))return false;
  const value=rates[marketCacheKey(asset.category,asset.code!)];
  return !value||value.annualReady!==true||value.requestedDays!==days||!Number.isFinite(value.annualRate);
 });
}

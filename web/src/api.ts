export type Status = 'unreviewed' | 'keep' | 'later' | 'cull';
export type Asset = { id:number; path:string; capturedAt:number; kind:string; source:string; size:number; status:Status; favourite:boolean; revision:number;alternativeCount:number;relatedCount?:number };
export type Page = {assets:Asset[];next:string};
export type Saved = {revision:number;previousStatus:Status;previousFavourite:boolean};
export async function get<T>(url:string,signal?:AbortSignal):Promise<T> {
  const r=await fetch(url,{signal});if(!r.ok)throw new Error('Could not load the catalogue. Check that the local service is running.');return r.json();
}
export async function decide(asset:Asset,status:Status,favourite:boolean,requestId:string):Promise<Saved>{
  // A retry uses exactly the same request body and idempotency key.
  const body=JSON.stringify({assetId:asset.id,status,favourite,expectedRevision:asset.revision,requestId});
  let r:Response;
  try{r=await fetch('/api/decisions',{method:'POST',headers:{'Content-Type':'application/json'},body})}
  catch{r=await fetch('/api/decisions',{method:'POST',headers:{'Content-Type':'application/json'},body})}
  if(!r.ok)throw new Error(r.status===409?'This decision changed in another session. Reload before deciding again.':'Save could not be confirmed. Reload to check the decision before continuing.');
  return r.json();
}

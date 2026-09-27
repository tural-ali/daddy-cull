export type Status = 'unreviewed' | 'keep' | 'later' | 'cull';
export type Asset = { id:number; path:string; capturedAt:number; kind:string; source:string; size:number; status:Status; favourite:boolean; revision:number;alternativeCount:number;relatedCount?:number;
  /** The other half of a RAW+JPEG pair, on the day pages. */
  pair?:number };
export type Page = {assets:Asset[];next:string};
export type Saved = {revision:number;previousStatus:Status;previousFavourite:boolean};
/** The frame listens for this and reads its counts again: the Bin badge, the
 * dates reviewed and the review streak. Anything that may have put a file into
 * the Bin or taken one out, or recorded a review, announces it, so the frame
 * never lags behind the page. */
export const BIN_CHANGED='cull:bin-changed';
export function binChanged(){window.dispatchEvent(new Event(BIN_CHANGED))}
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
  const saved:Saved=await r.json();
  binChanged();
  return saved;
}

export type Status = 'unreviewed' | 'keep' | 'later' | 'cull';
export type Asset = {comparisonReason?:string;comparisonPending?:boolean; id:number; path:string; capturedAt:number; kind:string; source:string; size:number; status:Status; favourite:boolean; revision:number;alternativeCount:number;relatedCount?:number;
  /** The other files of a RAW and its exports, on the day pages. */
  stack?:number[];
  /** Reached the archive after its day was reviewed, and still waits. */
  new?:boolean;
  /** A Live Photo: its clip plays from /api/media/{id}/live, and goes
   * wherever the photo goes. */
  live?:boolean;
  /** How long a video runs, in seconds, once the server has read it. */
  duration?:number;
  /** The picture's size as it is shown, once the server has read it. */
  width?:number; height?:number;
  /** Quarter turns clockwise the reviewer gave it in Cull, 1 to 3; the file
   * itself is as it was. Width and height are before the turn. */
  turn?:number;
  /** What Apple Photos made of the picture, on day pages, once the Mac has
   * sent its scores across and the file was found in them by name and day. */
  hint?:AssetHint };
/** Apple Photos' view of a file: evidence to weigh, never a decision. */
export type AssetHint = {source:'apple-photos';
  /** exact when Photos has the name on the same day, near when a day off. */
  how:'exact'|'near';
  /** Apple's overall aesthetic score, 0 to 1; absent when Photos never scored it. */
  overall?:number;
  /** Photos would pick it for Memories, which this reviewer mostly kept. */
  keep?:boolean;
  /** Photos marks it a failed shot, which this reviewer mostly removed. */
  cull?:boolean;
  /** Why, in words: "edited in Photos", "dark", "a face came out poorly". */
  reasons?:string[];
  faces?:number;
  /** What Photos saw in it, such as people or water. */
  labels?:string[];
  /** Apple's one-line description, when it has one. */
  caption?:string};
export type Page = {assets:Asset[];next:string};
/** What one file records about itself, read from the file on request. */
export type FileDetails = {camera?:string;lens?:string;shutter?:string;aperture?:number;iso?:number;focal?:number;software?:string;width?:number;height?:number;located:boolean;modified:string};
export type Saved = {revision:number;previousStatus:Status;previousFavourite:boolean};
/** The frame listens for this and reads its counts again: the Bin badge, the
 * dates reviewed and the review streak. Anything that may have put a file into
 * the Bin or taken one out, or recorded a review, announces it, so the frame
 * never lags behind the page. */
export const BIN_CHANGED='cull:bin-changed';
export function binChanged(){window.dispatchEvent(new Event(BIN_CHANGED))}
/** What the server said went wrong: every error from the API is JSON with an
 * `error` sentence to show, or `fallback` when the answer says nothing. */
export async function failure(response:Response,fallback:string){
  try{const body=await response.json() as {error?:string};return body.error||fallback}catch{return fallback}
}
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

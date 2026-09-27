import type {Asset} from '../api';

// A camera set to RAW+JPEG writes two files of each exposure. The day page
// shows the pair as one photo, the JPEG, with the RAW behind it: a choice on
// the photo is made for both files, and the viewer can show either. The
// server finds the pairs; the reviewer can split a wrong one.

/** The RAW behind each photo that shows for a pair, by the photo's id. A pair
 * shows as one only while both halves are on the page and decided alike, so a
 * RAW decided differently in the past is never hidden behind its JPEG. */
export function rawsBehind(assets:Asset[]):Map<number,Asset>{
  const byID=new Map(assets.map(asset=>[asset.id,asset]));
  const behind=new Map<number,Asset>();
  for(const asset of assets){
    if(!asset.pair||asset.kind==='raw')continue;
    const raw=byID.get(asset.pair);
    if(raw?.kind==='raw'&&raw.pair===asset.id&&raw.status===asset.status&&raw.favourite===asset.favourite)behind.set(asset.id,raw);
  }
  return behind;
}

/** "RAW+JPEG" or "RAW+HEIC", as Apple Photos names a pair. */
export function pairLabel(photo:Asset){
  const extension=photo.path.split('.').pop()?.toLowerCase()??'';
  return `RAW+${extension==='heic'||extension==='heif'?'HEIC':'JPEG'}`;
}

/** Splits a pair on the server, or joins it again. */
export async function setPaired(raw:number,partner:number,paired:boolean){
  const response=await fetch('/api/pairs',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({rawId:raw,partnerId:partner,paired})});
  if(!response.ok)throw new Error(paired?'The files could not be paired again.':'The files could not be unpaired.');
}

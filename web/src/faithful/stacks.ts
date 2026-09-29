import type {Asset} from '../api';

// A camera set to RAW+JPEG writes two files of each exposure, and an editor
// that exports a RAW writes its JPEG, HEIC or TIFF next to it. The day page
// shows such a stack as one photo, an export, with the RAW and any other
// exports behind it: a choice on the photo is made for every file, and the
// viewer can show each. The server finds the stacks, unless Settings shows
// the files apart; the reviewer can take a wrong export out of one.

/** The file's format as Apple Photos badges it: RAW for any camera RAW, the
 * extension for everything else. */
export function fileFormat(asset:Asset){
  const extension=asset.path.includes('.')?asset.path.split('.').pop()!.toUpperCase():'';
  return asset.kind==='raw'?'RAW':extension;
}

// Which export stands for a stack: the JPEG every browser draws, then HEIC,
// then TIFF.
const faceOrder=['JPG','JPEG','HEIC','HEIF','TIF','TIFF'];
function faceRank(asset:Asset){
  const rank=faceOrder.indexOf(fileFormat(asset));
  return asset.kind==='raw'||rank<0?faceOrder.length:rank;
}

/** The other files behind each photo that stands for a stack, by the photo's
 * id, the exports first and the RAW last. A stack shows as one only while all
 * its files are on the page and decided alike, so a file decided differently
 * in the past is never hidden behind another. */
export function stacksBehind(assets:Asset[]):Map<number,Asset[]>{
  const byID=new Map(assets.map(asset=>[asset.id,asset]));
  const behind=new Map<number,Asset[]>();
  const seen=new Set<number>();
  for(const asset of assets){
    if(!asset.stack?.length||seen.has(asset.id))continue;
    const files=[asset,...asset.stack.map(id=>byID.get(id))];
    files.forEach(file=>{if(file)seen.add(file.id)});
    if(files.some(file=>!file))continue;
    const stack=(files as Asset[]).sort((a,b)=>faceRank(a)-faceRank(b)||a.id-b.id);
    const face=stack[0];
    if(face.kind==='raw'||stack.filter(file=>file.kind==='raw').length!==1)continue;
    if(stack.some(file=>file.status!==face.status||file.favourite!==face.favourite||file.stack?.length!==stack.length-1))continue;
    behind.set(face.id,stack.slice(1));
  }
  return behind;
}

/** Every file of a stack, the photo that stands for it first. */
export function stackOf(photo:Asset,behind:Map<number,Asset[]>){
  return [photo,...behind.get(photo.id)??[]];
}

/** The formats a stack holds, as Apple Photos badges them: RAW first, then
 * each export, such as RAW, JPG and HEIC. */
export function stackFormats(files:Asset[]){
  const formats:string[]=files.map(fileFormat);
  return [...new Set([...formats.filter(format=>format==='RAW'),...formats.filter(format=>format!=='RAW')])];
}

/** Takes an export out of its RAW's stack on the server, or puts it back. */
export async function setPaired(raw:number,partner:number,paired:boolean){
  const response=await fetch('/api/pairs',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({rawId:raw,partnerId:partner,paired})});
  if(!response.ok)throw new Error(paired?'The files could not be stacked again.':'The files could not be shown apart.');
}

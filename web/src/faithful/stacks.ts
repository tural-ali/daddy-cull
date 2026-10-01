import type {Asset} from '../api';

// A camera set to RAW+JPEG writes two files of each exposure, and an editor
// that exports a RAW writes its JPEG, HEIC or TIFF next to it. An iPhone's
// HEIC often has a JPEG of the same shot beside it too. The day page shows
// such a stack as one photo, an export or the HEIC, with the other files
// behind it: a choice on the photo is made for every file, and the viewer can
// show each. The server finds the stacks, proving a HEIC and a JPEG one
// exposure by what both record, unless Settings shows the files apart; the
// reviewer can take a file out of one.

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

const heic=new Set(['HEIC','HEIF']),jpeg=new Set(['JPG','JPEG']);

/** The files of a stack, the one that stands for it first: of a RAW's, the
 * exports first and the RAW last; of a HEIC and a JPEG, the HEIC the camera
 * wrote. Undefined when the files are no stack the server makes. */
export function stackOrder(files:Asset[]):Asset[]|undefined{
  const raws=files.filter(file=>file.kind==='raw').length;
  if(raws===1){
    const stack=[...files].sort((a,b)=>faceRank(a)-faceRank(b)||a.id-b.id);
    return stack[0].kind==='raw'?undefined:stack;
  }
  const formats=files.map(fileFormat);
  if(raws===0&&files.length===2&&formats.some(format=>heic.has(format))&&formats.some(format=>jpeg.has(format)))
    return [...files].sort((a,b)=>Number(!heic.has(fileFormat(a)))-Number(!heic.has(fileFormat(b))));
  return undefined;
}

/** The file a stack is built on, its RAW or its HEIC: the server takes the
 * others out of it, or puts them back. */
export function stackLead(files:Asset[]){
  return files.find(file=>file.kind==='raw')??files.find(file=>heic.has(fileFormat(file)));
}

/** The other files behind each photo that stands for a stack, by the photo's
 * id, in stackOrder. A stack shows as one only while all its files are on the
 * page and decided alike, so a file decided differently in the past is never
 * hidden behind another. */
export function stacksBehind(assets:Asset[]):Map<number,Asset[]>{
  const byID=new Map(assets.map(asset=>[asset.id,asset]));
  const behind=new Map<number,Asset[]>();
  const seen=new Set<number>();
  for(const asset of assets){
    if(!asset.stack?.length||seen.has(asset.id))continue;
    const files=[asset,...asset.stack.map(id=>byID.get(id))];
    files.forEach(file=>{if(file)seen.add(file.id)});
    if(files.some(file=>!file))continue;
    const stack=stackOrder(files as Asset[]);
    if(!stack)continue;
    const face=stack[0];
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

/** Takes a file out of the stack built on lead on the server, or puts it
 * back. */
export async function setPaired(lead:number,partner:number,paired:boolean){
  const response=await fetch('/api/pairs',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({rawId:lead,partnerId:partner,paired})});
  if(!response.ok)throw new Error(paired?'The files could not be stacked again.':'The files could not be shown apart.');
}

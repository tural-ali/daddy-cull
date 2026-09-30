import {useState} from 'react';
import {Media} from '../Media';
import {binChanged,type Asset} from '../api';
import {Busy} from '../Busy';
import {Tile,bytes,dayLabel,keepOnly} from './Duplicates';
import {usePageActions} from './pageActions';

type UpgradeCopy={asset:Asset;pixels:string;ratio:number;date:string;album:string;available:boolean};
type UpgradeGroup={archive:Asset;day:string;pixels:string;accepted?:string;acceptedFrom?:string;acceptedAsset?:Asset;copies:UpgradeCopy[]};
export type UpgradePage={groups:UpgradeGroup[];total:number;pending:number;accepted:number;bytes:number};
type UpgradePlan={id:string;archiveAssetId:number;sourceAssetId:number;source:string;destination:string;size:number;hash:string;state:string;created:string;error?:string};

function name(value:string){return value.split('/').pop()||value}
function pixels(value:string){return value.replace('x','×')}
async function post<T>(url:string,body:unknown):Promise<T>{
  const response=await fetch(url,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
  const text=await response.text();
  let result:unknown;
  try{result=JSON.parse(text)}catch{throw new Error('The copy result could not be confirmed. Reload before retrying.')}
  if(!response.ok)throw new Error((result as {error?:string}).error||'The higher-resolution copy was not added.');
  binChanged();
  return result as T;
}

// A photo whose better copy is in the archive beside it: two files of one
// picture, of which one should stay.
type Pair={original:Asset;added:Asset;day:string;originalPixels:string;addedPixels:string;ratio:number};

type Rule='pixels'|'original';
const RULES:{key:Rule;label:string;hint:string}[]=[
  {key:'pixels',label:'More pixels',hint:'Keeps the higher-resolution copy added from Google and marks the original for the Bin.'},
  {key:'original',label:'The original',hint:'Keeps the file the archive had first and marks the added copy for the Bin.'},
];

function pairOf(group:UpgradeGroup):Pair|null{
  if(!group.acceptedAsset)return null;
  const from=group.copies.find(copy=>copy.asset.path===group.acceptedFrom)??group.copies[0];
  return {original:group.archive,added:group.acceptedAsset,day:group.day,originalPixels:group.pixels,addedPixels:from?.pixels??'',ratio:from?.ratio??0};
}

// Settled once one of the two is in the Bin and the other is not, as a
// duplicate group is.
const settled=(pair:Pair)=>(pair.original.status==='cull')!==(pair.added.status==='cull');

export function Upgrades({initial}:{initial:UpgradePage}){
  const [page,setPage]=useState(initial);
  const [rule,setRule]=useState<Rule>('pixels');
  const [overrides,setOverrides]=useState<Record<number,number>>({});
  const [skipped,setSkipped]=useState<Set<number>>(new Set());
  const [adding,setAdding]=useState<number|null>(null);
  const [saving,setSaving]=useState(false);
  const [message,setMessage]=useState('');
  const [error,setError]=useState('');
  const busy=saving||adding!==null;

  const pairs=page.groups.map(pairOf).filter((pair):pair is Pair=>pair!==null);
  const open=pairs.filter(pair=>!settled(pair));
  const done=pairs.length-open.length;
  // Added, but the catalogue has not indexed the new file yet.
  const waiting=page.groups.filter(group=>group.accepted&&!group.acceptedAsset);
  const toAdd=page.groups.filter(group=>!group.accepted);

  const keeperOf=(pair:Pair)=>{
    const picked=overrides[pair.original.id];
    if(picked!==undefined)return picked===pair.added.id?pair.added:pair.original;
    return rule==='original'?pair.original:pair.added;
  };
  const otherOf=(pair:Pair)=>keeperOf(pair).id===pair.added.id?pair.original:pair.added;
  const why=(pair:Pair)=>overrides[pair.original.id]!==undefined?'you picked it'
    :keeperOf(pair).id===pair.added.id?`it has ${pair.ratio.toFixed(1)}× the pixels of the original`:'it is the file the archive had first';

  const active=open.filter(pair=>!skipped.has(pair.original.id));
  const freeing=active.reduce((total,pair)=>total+otherOf(pair).size,0);

  async function resolve(chosen:Pair[]){
    if(busy||chosen.length===0)return;
    setSaving(true);setError('');setMessage('');
    try{
      // The picture was a favourite whichever file was marked, so the one
      // that stays carries it.
      await keepOnly(chosen.map(pair=>({keeper:keeperOf(pair),members:[pair.original,pair.added],favourite:pair.original.favourite||pair.added.favourite})));
      const decided=new Map(chosen.map(pair=>[pair.original.id,keeperOf(pair).id]));
      // A settled pair leaves the page, so only the status matters from here.
      const mark=(asset:Asset,keeper:number):Asset=>({...asset,status:asset.id===keeper?'keep':'cull'});
      setPage(value=>({...value,groups:value.groups.map(group=>{
        const keeper=decided.get(group.archive.id);
        return keeper===undefined||!group.acceptedAsset?group:{...group,archive:mark(group.archive,keeper),acceptedAsset:mark(group.acceptedAsset,keeper)};
      })}));
      const freed=chosen.reduce((total,pair)=>total+otherOf(pair).size,0);
      setMessage(`${chosen.length} ${chosen.length===1?'copy':'copies'} marked for the Bin, ${bytes(freed)} in all. Nothing has moved yet: open Bin to carry it out, and it stays restorable after that.`);
    }catch(reason){setError((reason as Error).message)}finally{setSaving(false)}
  }

  async function accept(group:UpgradeGroup,copy:UpgradeCopy){
    setAdding(group.archive.id);setMessage('');setError('');
    try{
      const preview=await post<UpgradePlan>('/api/upgrade-actions/preview',{archiveAssetId:group.archive.id,sourceAssetId:copy.asset.id});
      const result=await post<UpgradePlan>('/api/upgrade-actions/execute',{id:preview.id});
      setPage(value=>({...value,pending:Math.max(0,value.pending-1),accepted:value.accepted+1,bytes:value.bytes+result.size,groups:value.groups.map(item=>item.archive.id===group.archive.id?{...item,accepted:`/archive/${result.destination}`,acceptedFrom:copy.asset.path}:item)}));
      setMessage(`Added ${name(result.destination)} beside the archive copy. The Google source was retained. Once the catalogue has indexed it, choose which of the two to keep.`);
    }catch(reason){setError((reason as Error).message)}finally{setAdding(null)}
  }

  usePageActions(open.length>0?{actions:[{label:`Merge all ${active.length.toLocaleString()}`,short:'Merge all',icon:'filter_none',keys:'Shift+M',primary:true,disabled:busy||active.length===0,onClick:()=>void resolve(active)}]}:null);

  return <section className="dupehead dupepage">
    <h1>Upgrades</h1>
    <p className="ysum"><b>{open.length.toLocaleString()}</b> {open.length===1?'pair':'pairs'} to settle · <b>{bytes(freeing)}</b> can be freed
      <span className="dim"> · {page.accepted.toLocaleString()} {page.accepted===1?'copy':'copies'} added so far, {bytes(page.bytes)}{done>0&&` · ${done.toLocaleString()} settled`}</span></p>
    <p className="hint">Each pair is a photo from the library and the higher-resolution copy of it added beside it from Google Takeout. Merging keeps the ticked file and marks the other for the Bin, where it stays restorable. Click the other file to keep it instead.</p>

    {open.length>0&&<div className="dupebulk">
      <label className="dupekeep">Keep
        <select value={rule} disabled={busy} onChange={event=>{setRule(event.target.value as Rule);setOverrides({})}}>
          {RULES.map(entry=><option key={entry.key} value={entry.key} title={entry.hint}>{entry.label.toLowerCase()}</option>)}
        </select>
      </label>
      <span className="dupespace">
        <span><b>{active.length.toLocaleString()}</b> {active.length===1?'file':'files'} for the Bin, <b>{bytes(freeing)}</b></span>
      </span>
    </div>}
    {saving&&<p className="flash" role="status"><Busy label="Saving…" state="working"/></p>}
    {message&&<p className="flash" role="status">{message}</p>}
    {error&&<p className="note warn" role="alert">{error}</p>}
    {page.groups.length===0&&<p className="empty">No confirmed upgrades are indexed.</p>}
    {pairs.length>0&&open.length===0&&<p className="note ok">Every upgraded photo is settled: one of each pair is in the Bin.</p>}

    {open.map(pair=>{
      const id=pair.original.id;
      const keeper=keeperOf(pair);
      const skip=skipped.has(id);
      return <article className={`dupegroup${skip?' skipped':''}`} key={id}>
        <header className="dupegrouphead">
          <h2>{dayLabel(pair.day)}</h2>
          {pair.ratio>0&&<span className="dupeproof">{pair.ratio.toFixed(1)}× the pixels</span>}
          <button type="button" className="dupelink" disabled={busy||skip} title={`Keeps ${name(keeper.path)} because ${why(pair)}, and marks ${name(otherOf(pair).path)} for the Bin`} onClick={()=>void resolve([pair])}>Merge 2 copies</button>
          <button type="button" className="dupelink quiet" disabled={busy} onClick={()=>setSkipped(current=>{const next=new Set(current);if(next.has(id))next.delete(id);else next.add(id);return next})}>{skip?'Include':'Skip'}</button>
        </header>
        <ul className="dupetiles">{[{file:pair.original,size:pair.originalPixels},{file:pair.added,size:pair.addedPixels}].map(({file,size})=>
          <Tile key={file.id} member={file} previewID={file.id} label={name(file.path)} detail={size?pixels(size):undefined}
            keeper={file.id===keeper.id} placeless={false} disabled={busy||skip}
            onKeep={()=>setOverrides(current=>({...current,[id]:file.id}))}/>)}</ul>
      </article>;
    })}

    {waiting.length>0&&<>
      <h2 className="binsec">Added, not catalogued yet</h2>
      <p className="hint">These copies are in the archive, but the catalogue has not indexed them yet, so there is nothing to choose between until it does.</p>
      <ul className="plain mono">{waiting.map(group=><li key={group.archive.id}>{name(group.accepted??'')} <span className="dim">· beside {name(group.archive.path)}</span></li>)}</ul>
    </>}

    {toAdd.length>0&&<>
      <h2 className="binsec">Not added yet</h2>
      <p className="hint">Each is a photo in the library beside a copy of it from Google Takeout with more pixels. Adding saves the copy next to the original with “(hi-res)” in its name; nothing is replaced.</p>
      <div className="uppairs">{toAdd.map(group=>{
        const best=group.copies.find(copy=>copy.available)||group.copies[0];
        if(!best)return null;
        const extra=group.copies.length-1;
        return <section className="pair" key={group.archive.id}>
          <div className="phead"><span className="gnum">{best.ratio.toFixed(1)}×</span><strong title={name(group.archive.path)}>{name(group.archive.path)}</strong><span className="meta">{group.day}</span></div>
          <div className="sides">
            <figure className="upcopy"><a href={`/api/media/${group.archive.id}/original`} target="_blank" rel="noreferrer"><Media asset={group.archive}/></a><figcaption><span className="fn">in the archive</span><span className="sz">{pixels(group.pixels)}</span></figcaption></figure>
            <figure className="upcopy better"><a href={`/api/media/${best.asset.id}/original`} target="_blank" rel="noreferrer"><Media asset={best.asset}/></a><figcaption><span className="fn">Google · {best.album}</span><span className="sz">{pixels(best.pixels)}</span>{!best.available&&<span className="tag other">file missing</span>}{extra>0&&<span className="tag other">+{extra} album {extra===1?'copy':'copies'}</span>}</figcaption></figure>
          </div>
          {best.available&&<div className="gact"><button className="btn primary" disabled={busy} onClick={()=>void accept(group,best)}>{adding===group.archive.id?<Busy label="Verifying and copying…" state="working"/>:'Add the higher-resolution copy'}</button></div>}
        </section>;
      })}</div>
    </>}
  </section>;
}

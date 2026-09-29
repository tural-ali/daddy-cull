import {useState} from 'react';
import {Media} from '../Media';
import {binChanged,type Asset} from '../api';
import {Busy} from '../Busy';

type UpgradeCopy={asset:Asset;pixels:string;ratio:number;date:string;album:string;available:boolean};
type UpgradeGroup={archive:Asset;day:string;pixels:string;accepted?:string;acceptedAsset?:Asset;copies:UpgradeCopy[]};
export type UpgradePage={groups:UpgradeGroup[];total:number;pending:number;accepted:number;bytes:number};
type UpgradePlan={id:string;archiveAssetId:number;sourceAssetId:number;source:string;destination:string;size:number;hash:string;state:string;created:string;error?:string};

function bytes(value:number){return value<1024**2?`${(value/1024).toFixed(1)} KB`:value<1024**3?`${(value/1024**2).toFixed(1)} MB`:`${(value/1024**3).toFixed(1)} GB`}
function name(value:string){return value.split('/').pop()||value}
async function post<T>(url:string,body:unknown):Promise<T>{
  const response=await fetch(url,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
  const text=await response.text();
  let result:unknown;
  try{result=JSON.parse(text)}catch{throw new Error('The copy result could not be confirmed. Reload before retrying.')}
  if(!response.ok)throw new Error((result as {error?:string}).error||'The higher-resolution copy was not added.');
  binChanged();
  return result as T;
}

export function Upgrades({initial}:{initial:UpgradePage}){
  const [page,setPage]=useState(initial);
  const [busy,setBusy]=useState<number|null>(null);
  const [message,setMessage]=useState('');
  const [error,setError]=useState('');
  async function accept(group:UpgradeGroup,copy:UpgradeCopy){
    setBusy(group.archive.id);setMessage('');setError('');
    try{
      const preview=await post<UpgradePlan>('/api/upgrade-actions/preview',{archiveAssetId:group.archive.id,sourceAssetId:copy.asset.id});
      const result=await post<UpgradePlan>('/api/upgrade-actions/execute',{id:preview.id});
      setPage(value=>({...value,pending:Math.max(0,value.pending-1),accepted:value.accepted+1,bytes:value.bytes+result.size,groups:value.groups.map(item=>item.archive.id===group.archive.id?{...item,accepted:`/archive/${result.destination}`}:item)}));
      setMessage(`Added ${name(result.destination)} beside the archive copy. The Google source was retained.`);
    }catch(reason){setError((reason as Error).message)}finally{setBusy(null)}
  }
  return <>
    <section className="dupehead">
      <h1>Upgrades</h1>
      <p className="ysum"><b>{page.total.toLocaleString()}</b> photos with a better copy · <b>{page.pending.toLocaleString()}</b> still to decide · <b>{page.accepted.toLocaleString()}</b> added so far, <b>{bytes(page.bytes)}</b></p>
    </section>
    {message&&<p className="flash" role="status">{message}</p>}
    {error&&<p className="note warn" role="alert">{error}</p>}
    {page.groups.length===0&&<p className="empty">No confirmed upgrades are indexed.</p>}
    <div className="uppairs">{page.groups.map(group=>{
      const best=group.copies.find(copy=>copy.available)||group.copies[0];
      if(!best)return null;
      const extra=group.copies.length-1;
      const rightAsset=group.acceptedAsset||best.asset;
      return <section className={`pair${group.accepted?' settled':''}`} key={group.archive.id}>
        <div className="phead"><span className="gnum">{best.ratio.toFixed(1)}×</span><strong title={name(group.archive.path)}>{name(group.archive.path)}</strong><span className="meta">{group.day}</span>{group.accepted&&<span className="tag done" title={`Added as ${name(group.accepted)}`}>Added</span>}</div>
        <div className="sides">
          <figure className="upcopy"><a href={`/api/media/${group.archive.id}/original`} target="_blank" rel="noreferrer"><Media asset={group.archive}/></a><figcaption><span className="fn">in the archive</span><span className="sz">{group.pixels}</span></figcaption></figure>
          <figure className="upcopy better"><a href={`/api/media/${rightAsset.id}/original`} target="_blank" rel="noreferrer"><Media asset={rightAsset}/></a><figcaption><span className="fn">{group.accepted?'added to the archive':`Google · ${best.album}`}</span><span className="sz">{best.pixels}</span>{!group.accepted&&!best.available&&<span className="tag other">file missing</span>}{extra>0&&<span className="tag other">+{extra} album {extra===1?'copy':'copies'}</span>}</figcaption></figure>
        </div>
        {!group.accepted&&best.available&&<div className="gact"><button className="btn primary" disabled={busy!==null} onClick={()=>void accept(group,best)}>{busy===group.archive.id?<Busy label="Verifying and copying…" state="working"/>:'Add the higher-resolution copy'}</button></div>}
      </section>;
    })}</div>
  </>;
}

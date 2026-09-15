import {useState} from 'react';
import {Media} from '../Media';
import type {Asset} from '../api';

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
    <div className="summary">
      <div className="stat"><b>{page.total.toLocaleString()}</b><span>photos with a better copy</span></div>
      <div className="stat"><b>{page.pending.toLocaleString()}</b><span>still to decide</span></div>
      <div className="stat"><b>{page.accepted.toLocaleString()}</b><span>added so far</span></div>
      <div className="stat"><b>{bytes(page.bytes)}</b><span>added to the archive</span></div>
    </div>
    <p className="meta">Each pair is the same photograph: the archive copy on the left and Google’s higher-resolution copy on the right. Adding creates <strong>NAME (hi-res).ext</strong> beside the original. It never replaces or deletes either source.</p>
    {message&&<p className="flash" role="status">{message}</p>}
    {error&&<p className="note warn" role="alert">{error}</p>}
    {page.groups.length===0&&<p className="empty">No confirmed upgrades are indexed.</p>}
    {page.groups.map(group=>{
      const best=group.copies.find(copy=>copy.available)||group.copies[0];
      if(!best)return null;
      const extra=group.copies.length-1;
      const rightAsset=group.acceptedAsset||best.asset;
      return <section className={`pair${group.accepted?' settled':''}`} key={group.archive.id}>
        <div className="phead"><span className="gnum">{best.ratio.toFixed(1)}×</span> <strong>{name(group.archive.path)}</strong> <span className="meta">{group.day} · captured {best.date.slice(0,10).replaceAll(':','-')}</span>{group.accepted&&<span className="tag done"> added as {name(group.accepted)}</span>}</div>
        <div className="sides">
          <figure className="card side"><a href={`/api/media/${group.archive.id}/original`} target="_blank" rel="noreferrer"><Media asset={group.archive}/></a><figcaption><span className="fn">in the archive</span><span className="sz">{group.pixels}</span></figcaption></figure>
          <figure className="card side better"><a href={`/api/media/${rightAsset.id}/original`} target="_blank" rel="noreferrer"><Media asset={rightAsset}/></a><figcaption><span className="fn">{group.accepted?'added to the archive':`Google · ${best.album}`}</span><span className="sz">{best.pixels}</span><span className="tag flag">{best.ratio.toFixed(1)}× pixels</span>{!group.accepted&&!best.available&&<span className="tag other">file missing</span>}{extra>0&&<span className="tag other">+{extra} album {extra===1?'copy':'copies'}</span>}</figcaption></figure>
        </div>
        {!group.accepted&&best.available&&<div className="gact"><button className="primary" disabled={busy!==null} onClick={()=>void accept(group,best)}>{busy===group.archive.id?'Verifying and copying…':'Add the higher-resolution copy'}</button><span className="hint">Copied and SHA-256 verified beside the original. The Google file stays untouched.</span></div>}
      </section>;
    })}
  </>;
}

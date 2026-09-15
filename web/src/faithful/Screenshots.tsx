import {Media} from '../Media';
import type {Asset} from '../api';
import {useState} from 'react';

type ScreenshotItem=Asset&{day:string;name:string;state:string};
export type ScreenshotPage={items:ScreenshotItem[];total:number;bytes:number};
export type ScreenshotPlan={id:string;assetId:number;action:'keep'|'remove';state:string;created:string;files:{source:string;destination:string;size:number;hash:string;sidecar:boolean;phase:string}[];error?:string};
function bytes(value:number){return value<1024**2?`${(value/1024).toFixed(1)} KB`:value<1024**3?`${(value/1024**2).toFixed(1)} MB`:`${(value/1024**3).toFixed(1)} GB`}
async function post<T>(path:string,body:unknown):Promise<T>{
  const response=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
  const text=await response.text();
  let result:unknown;
  try{result=JSON.parse(text)}catch{throw new Error('The screenshot result could not be confirmed. Reload before retrying.')}
  if(!response.ok)throw new Error((result as {error?:string}).error||'The screenshot action failed safely.');
  return result as T;
}

export function Screenshots({page,filter,from}:{page:ScreenshotPage;filter:string;from:number}){
  const [current,setCurrent]=useState(page);
  const [busy,setBusy]=useState<number|null>(null);
  const [message,setMessage]=useState('');
  const [error,setError]=useState('');
  const [undo,setUndo]=useState<ScreenshotPlan|null>(null);
  const {items,total,bytes:totalBytes}=current;
  const per=120,to=Math.min(total,from+items.length);
  const href=(kind:string,offset:number)=>`/screenshots?show=${kind}&from=${offset}`;
  async function act(item:ScreenshotItem,action:'keep'|'remove'){
    setBusy(item.id);setError('');setMessage('');
    try{
      const preview=await post<ScreenshotPlan>('/api/screenshot-actions/preview',{assetId:item.id,action});
      const result=await post<ScreenshotPlan>('/api/screenshot-actions/execute',{id:preview.id});
      setCurrent(value=>({...value,items:value.items.filter(candidate=>candidate.id!==item.id),total:value.total-1,bytes:value.bytes-item.size}));
      setUndo(action==='remove'?result:null);
      setMessage(action==='keep'?`Kept ${item.name} in ${result.files[0].destination}.`:`Removed ${item.name} to the recoverable Bin.`);
    }catch(reason){setError((reason as Error).message)}finally{setBusy(null)}
  }
  async function restore(){
    if(!undo)return;
    setBusy(undo.assetId);setError('');
    try{await post<ScreenshotPlan>('/api/screenshot-actions/undo',{id:undo.id});location.reload()}catch(reason){setError((reason as Error).message);setBusy(null)}
  }
  return <>
    <section className="dupehead"><h1>Screenshots</h1><p className="ysum"><b>{total.toLocaleString()}</b> waiting · <b>{bytes(totalBytes)}</b>{total>0&&<span className="dim"> · showing {(from+1).toLocaleString()}–{to.toLocaleString()}</span>}</p><p className="pager">{[['','Everything'],['image','Stills'],['video','Recordings']].map(([kind,label])=><a className={`btn small${filter===kind?' on':''}`} href={href(kind,0)} key={label}>{label}</a>)}{from>0&&<a className="btn small" href={href(filter,Math.max(0,from-per))}>← Previous</a>}{to<total&&<a className="btn small" href={href(filter,from+per)}>Next →</a>}</p><p className="note">Nothing here is in the archive. <strong>Remove</strong> sends it to the recoverable Bin. <strong>Keep</strong> verifies and puts it back under the date in its filename. Sidecars travel with their file, and an existing archive file is never overwritten.</p></section>
    {message&&<p className="flash" role="status">{message} {undo&&<button className="btn small" disabled={busy!==null} onClick={()=>void restore()}>Undo remove</button>}</p>}{error&&<p className="note warn" role="alert">{error}</p>}
    {items.length===0?<p className="note">{total===0?'The holding area is empty.':'Nothing matches this filter.'}</p>:<div className="gal tight shots">{items.map(item=><figure className="mo" key={item.id}><Media asset={item}/><div className="bdg">{item.kind==='video'&&<span className="b">recording</span>}{!item.day&&<span className="b warn">undateable</span>}</div><div className="acts"><button type="button" className="act cull" disabled={busy!==null} onClick={()=>void act(item,'remove')}>{busy===item.id?'Working…':'Remove'}</button>{item.day&&<button type="button" className="act keep" disabled={busy!==null} onClick={()=>void act(item,'keep')}>Keep</button>}</div><figcaption className="cap"><span>{item.day||'undated'}</span><span className="dim">{bytes(item.size)}</span></figcaption></figure>)}</div>}
    {items.length>0&&<p className="pager">{from>0&&<a className="btn small" href={href(filter,Math.max(0,from-per))}>← Previous</a>}{to<total&&<a className="btn small" href={href(filter,from+per)}>Next {per} →</a>}</p>}
  </>;
}

import {useState} from 'react';
import {Media} from '../Media';
import type {DuplicateGroup} from './Today';

export type {DuplicateGroup} from './Today';

function requestID(){return Array.from(crypto.getRandomValues(new Uint8Array(16)),byte=>byte.toString(16).padStart(2,'0')).join('')}
function bytes(value:number){return value<1024**2?`${(value/1024).toFixed(1)} KB`:value<1024**3?`${(value/1024**2).toFixed(1)} MB`:`${(value/1024**3).toFixed(1)} GB`}

export function Duplicates({initial,hashed}:{initial:DuplicateGroup[];hashed:number}){
  const [groups,setGroups]=useState(initial);
  const [keepers,setKeepers]=useState<Record<string,number>>(()=>Object.fromEntries(initial.map(group=>[group.hash,group.members[0].id])));
  const [busy,setBusy]=useState(false);
  const [message,setMessage]=useState('');
  async function resolve(group:DuplicateGroup){
    if(busy)return;
    const keeperID=keepers[group.hash]??group.members[0].id;
    const changes=group.members.map(asset=>({assetId:asset.id,status:asset.id===keeperID?'keep':'cull',favourite:asset.favourite,expectedRevision:asset.revision,requestId:requestID()}));
    setBusy(true);
    setMessage('Saving the duplicate choices…');
    try{
      const response=await fetch('/api/decisions/batch',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(changes)});
      if(!response.ok)throw new Error(response.status===409?'One of these files changed. Reload before resolving this group.':'The duplicate choices could not be confirmed.');
      setGroups(current=>current.filter(item=>item.hash!==group.hash||item.size!==group.size));
      setMessage(`${group.members.length-1} verified ${group.members.length===2?'copy':'copies'} marked for the Bin. No file has moved.`);
    }catch(error){setMessage((error as Error).message)}finally{setBusy(false)}
  }
  const reclaimable=groups.reduce((total,group)=>total+group.reclaimable,0);
  return <section className="dupehead">
    <h1>Exact duplicates</h1>
    <p className="ysum"><b>{groups.length.toLocaleString()}</b> verified groups · <b>{bytes(reclaimable)}</b> reclaimable</p>
    <p className="hint">Every group is byte-identical and backed by a cached full hash. Choose the copy whose folder placement you want to keep.</p>
    {message&&<p className="flash" role="status">{message}</p>}
    {groups.length===0?(hashed===0
      ? <p className="note warn">No file in this catalogue has a cached full hash yet, so this page cannot tell you whether duplicates exist. It is empty because the evidence is missing, not because the archive is clean. Import hash evidence before reading anything into this.</p>
      : <p className="note ok">No unresolved byte-identical duplicates among the {hashed.toLocaleString()} fully hashed files.</p>):groups.map((group,index)=><div className="xgroup" key={`${group.hash}:${group.size}`}>
      <p className="xmeta"><span className="gnum">{index+1}</span>{group.members.length} identical copies · {bytes(group.size)} each · <strong>{bytes(group.reclaimable)}</strong> reclaimable <span className="hash">{group.hash}</span></p>
      <div className="gal tight">{group.members.map(member=><figure className={`mo${(keepers[group.hash]??group.members[0].id)===member.id?' keeper':''}`} key={member.id}>
        <Media asset={member}/>
        <div className="bdg"><button type="button" className="b tocmp" disabled={busy} onClick={()=>setKeepers(current=>({...current,[group.hash]:member.id}))}>{(keepers[group.hash]??group.members[0].id)===member.id?'keep this one':'choose as keeper'}</button></div>
        <figcaption className="cap"><span>{member.day}</span><span className="dim">{member.path.split('/').pop()}</span></figcaption>
      </figure>)}</div>
      <p className="xact"><button className="btn small danger" disabled={busy} onClick={()=>void resolve(group)}>Keep the selected copy, mark the other {group.members.length-1} for the Bin</button><span className="hint">The Bin remains separately reviewable and restorable.</span></p>
    </div>)}
  </section>;
}

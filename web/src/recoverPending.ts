// Replays durable decisions left by a closed tab. Request IDs make concurrent
// replay harmless; entries are removed only after server acknowledgement.
export async function recoverPending(){
 const keys=Object.keys(localStorage).filter(k=>k.startsWith('cull.group.pending.')||k.startsWith('cull.pending.'));
 for(const key of keys){
  const raw=localStorage.getItem(key);if(!raw)continue;
  if(key.startsWith('cull.group.pending.')){
   const jobs=JSON.parse(raw);if(!Array.isArray(jobs)||!jobs.length||jobs.length>20)throw Error('A stored group choice needs recovery.');
   const r=await fetch('/api/decisions/batch',{method:'POST',headers:{'Content-Type':'application/json'},body:raw});
   if(!r.ok)throw Error('A previous group save could not be confirmed. Retry recovery before reviewing.');
   if(localStorage.getItem(key)===raw)localStorage.removeItem(key);
  }else{
   const jobs=JSON.parse(raw);if(!Array.isArray(jobs))throw Error('Stored choices need recovery.');
   for(const j of jobs){
    if(!j.asset?.id||!j.requestId)throw Error('A stored choice needs recovery.');
    const r=await fetch('/api/decisions',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({assetId:j.asset.id,status:j.status,favourite:j.favourite,expectedRevision:j.asset.revision,requestId:j.requestId})});
    if(!r.ok)throw Error('A previous save could not be confirmed. Retry recovery before reviewing.');
    const remaining=JSON.parse(localStorage.getItem(key)||'[]');localStorage.setItem(key,JSON.stringify(remaining.filter((x:{requestId:string})=>x.requestId!==j.requestId)));
   }
  }
 }
}

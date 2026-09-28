import {binChanged,type Status} from '../api';
import {tracked} from '../saving';

export function requestID(){return Array.from(crypto.getRandomValues(new Uint8Array(16)),byte=>byte.toString(16).padStart(2,'0')).join('')}

export type Change={assetId:number;status:Status;favourite:boolean;expectedRevision:number;requestId:string};

// The server takes twenty decisions per request, so a selection goes as several
// batches in order. A failed batch stops the rest and the message says how many
// were already saved, because a half-applied selection the reviewer cannot see
// is worse than one they can.
const PER_REQUEST=20;

export function sendDecisions(changes:Change[]):Promise<{revision:number}[]>{
  return tracked(send(changes));
}

async function send(changes:Change[]):Promise<{revision:number}[]>{
  const saved:{revision:number}[]=[];
  for(let i=0;i<changes.length;i+=PER_REQUEST){
    const response=await fetch('/api/decisions/batch',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(changes.slice(i,i+PER_REQUEST))});
    if(!response.ok)throw new Error(response.status===409
      ?`One of these files changed in another session. ${saved.length} saved; reload before deciding again.`
      :`The decision could not be confirmed. ${saved.length} saved; reload to check before continuing.`);
    saved.push(...await response.json() as {revision:number}[]);
  }
  binChanged();
  return saved;
}

/** The decisions that put each file back as it was, for an Undo button. */
export function reverting(before:{id:number;status:Status;favourite:boolean}[],saved:{revision:number}[]):Change[]{
  return before.map((item,index)=>({assetId:item.id,status:item.status,favourite:item.favourite,expectedRevision:saved[index].revision,requestId:requestID()}));
}

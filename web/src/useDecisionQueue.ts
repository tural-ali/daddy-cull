import {useEffect,useRef,useState} from 'react';
import {type Asset,type Saved,type Status,decide} from './api';
export type PendingDecision={asset:Asset;status:Status;favourite:boolean;requestId:string;wasResolved:boolean};
const randomId=()=>Array.from(crypto.getRandomValues(new Uint8Array(16)),b=>b.toString(16).padStart(2,'0')).join('');

// Each tab owns its journal, so two tabs cannot overwrite each other's queue.
// The journal survives refresh and is removed only after server acknowledgement.
// onSaved is told whether a later choice on the same file is still waiting,
// in which case that one, not this, is what the file shows.
export function useDecisionQueue(onSaved:(job:PendingDecision,result:Saved,overtaken:boolean)=>void){
 const jobs=useRef<PendingDecision[]>([]);const running=useRef(false);const paused=useRef(false);
 const key=useRef('');const callback=useRef(onSaved);callback.current=onSaved;
 const [pending,setPending]=useState(0);const [error,setError]=useState('');const [ready,setReady]=useState(false);
 function persist(next:PendingDecision[]){localStorage.setItem(key.current,JSON.stringify(next));jobs.current=next;setPending(next.length)}
 async function drain(){
  if(running.current||paused.current)return;running.current=true;
  try{while(jobs.current.length){
   const job=jobs.current[0];
   const result=await decide(job.asset,job.status,job.favourite,job.requestId);
   // A later choice on the same file was queued before this one was
   // confirmed, so it waits for, and builds on, the revision just saved.
   const rest=jobs.current.slice(1);
   persist(rest.map(next=>next.asset.id===job.asset.id?{...next,asset:{...next.asset,revision:result.revision}}:next));setError('');
   callback.current(job,result,rest.some(next=>next.asset.id===job.asset.id));
  }}catch(e){paused.current=true;setError(`A choice has not been confirmed. Your pending choices are retained locally. ${(e as Error).message}`)}
  finally{running.current=false}
 }
 useEffect(()=>{
  try{
   let tab=sessionStorage.getItem('cull.tab');if(!tab){tab=randomId();sessionStorage.setItem('cull.tab',tab)}
   key.current='cull.pending.'+tab;
   const saved=JSON.parse(localStorage.getItem(key.current)||'[]');
   if(!Array.isArray(saved)||saved.some(j=>!j?.asset?.id||!j.requestId||!['keep','later','cull','unreviewed'].includes(j.status)))throw Error('Pending-choice journal needs recovery.');
   jobs.current=saved;setPending(saved.length);setReady(true);void drain();
  }catch(e){paused.current=true;setError((e as Error).message)}
  const leave=(e:BeforeUnloadEvent)=>{if(jobs.current.length){e.preventDefault();e.returnValue=''}};
  window.addEventListener('beforeunload',leave);return()=>window.removeEventListener('beforeunload',leave);
 },[]);
 function enqueue(job:Omit<PendingDecision,'requestId'>){
  if(!ready||paused.current)return false;
  if(jobs.current.length>=32){setError('32 choices are waiting to save. Wait for the connection to catch up.');return false}
  try{persist([...jobs.current,{...job,requestId:randomId()}]);setError('');void drain();return true}
  catch{paused.current=true;setError('Could not retain the choice locally. Review is paused; no choice was advanced.');return false}
 }
 return {enqueue,pending,error,ready,retry:()=>{paused.current=false;setError('');void drain()}};
}

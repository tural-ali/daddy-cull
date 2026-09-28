import {useEffect,useRef,useState} from 'react';
import {HISTORY_LIMIT,historyKey} from './history';
import {currentVisit,reloadPage} from './router';
import {requestID,sendDecisions} from './decisions';
import type {Status} from '../api';

// Undo and redo for a list page such as Screenshots, where what was done is
// carried out on the server and the page is then read again. The history is
// kept for the page visit rather than in the page, so reading the page again
// after an undo does not forget the steps before it, and Back to the page
// finds them still there. ⌘Z undoes, ⇧⌘Z or ⌘U redoes, as on a date.

/** One thing done, and how to take it back and do it again. Each is run on
 * the server; a step that fails is left where it was. */
export type Undoable={label:string;undo:()=>Promise<void>;redo:()=>Promise<void>};

type Stack={past:Undoable[];future:Undoable[];notice:string};
const stacks=new Map<number,Stack>();
function stackFor(visit:number){
  let stack=stacks.get(visit);
  if(!stack){stack={past:[],future:[],notice:''};stacks.set(visit,stack)}
  return stack;
}

/** A page's undo history. `busy` holds a key pressed while the page is still
 * saving until it is done, rather than losing it. */
export function usePageUndo(busy:boolean,onError:(message:string)=>void){
  const [visit]=useState(currentVisit);
  const stack=stackFor(visit);
  const [,redraw]=useState(0);
  // What the last undo or redo did, said once after the page is read again.
  const [notice,setNotice]=useState(stack.notice);
  useEffect(()=>{stack.notice=''},[stack]);
  const [working,setWorking]=useState(false);
  const held=useRef<'undo'|'redo'|null>(null);

  function record(entry:Undoable){
    stack.past=[...stack.past.slice(-(HISTORY_LIMIT-1)),entry];
    stack.future=[];
    setNotice('');
    redraw(n=>n+1);
  }
  async function step(direction:'undo'|'redo'){
    if(working)return;
    if(busy){held.current=direction;return}
    const from=direction==='undo'?stack.past:stack.future,to=direction==='undo'?stack.future:stack.past;
    const entry=from.pop();
    if(!entry){setNotice(`Nothing to ${direction}.`);return}
    setWorking(true);setNotice('');
    try{
      await entry[direction]();
      to.push(entry);
      stack.notice=`${direction==='undo'?'Undone':'Redone'}: ${entry.label}.`;
      reloadPage();
    }catch(reason){
      from.push(entry);
      onError((reason as Error).message);
    }finally{setWorking(false);redraw(n=>n+1)}
  }
  const latest=useRef(step);
  latest.current=step;
  useEffect(()=>{
    if(busy||!held.current)return;
    const direction=held.current;
    held.current=null;
    void latest.current(direction);
  },[busy]);
  useEffect(()=>{
    function key(event:KeyboardEvent){
      const action=historyKey(event);
      if(!action)return;
      event.preventDefault();
      if(!event.repeat)void latest.current(action);
    }
    window.addEventListener('keydown',key);
    return()=>window.removeEventListener('keydown',key);
  },[]);
  return {record,undo:()=>void step('undo'),redo:()=>void step('redo'),latest:stack.past.at(-1)?.label??null,canRedo:stack.future.length>0,notice,working};
}

type Decided={id:number;status:Status;favourite:boolean};

/** Decisions as an undoable step: `before` is how each file was, `after` how
 * it was left, `saved` the revisions it was left at. Each step goes on from
 * the revisions the last one returned, so a change made meanwhile elsewhere
 * is refused rather than overwritten. */
export function undoableDecisions(label:string,before:Decided[],after:Decided[],saved:{revision:number}[]):Undoable{
  let revisions=saved.map(entry=>entry.revision);
  async function apply(target:Decided[]){
    const result=await sendDecisions(target.map((item,index)=>({assetId:item.id,status:item.status,favourite:item.favourite,expectedRevision:revisions[index],requestId:requestID()})));
    revisions=result.map(entry=>entry.revision);
  }
  return {label,undo:()=>apply(before),redo:()=>apply(after)};
}

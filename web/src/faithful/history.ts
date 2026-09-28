import {useCallback,useRef,useState} from 'react';
import type {Status} from '../api';

/** One file's decision at a moment: enough to put it back that way. */
export type Snapshot={id:number;status:Status;favourite:boolean};
export type HistoryEntry=
  |{kind:'decisions';label:string;before:Snapshot[];after:Snapshot[]}
  |{kind:'progress';label:string;days:string[];before:'pending'|'done';after:'pending'|'done';kept?:number[]}
  |{kind:'pair';label:string;raw:number;partner:number;before:boolean;after:boolean}
  |{kind:'turn';label:string;ids:number[];quarters:number};

/** How far back a day's review can be unwound. */
export const HISTORY_LIMIT=100;

/** The undo key, named the way the keyboard shows it. */
export const undoKeys=/Mac|iPhone|iPad/.test(navigator.platform)?{undo:'⌘Z',redo:'⌘U'}:{undo:'Ctrl+Z',redo:'Ctrl+U'};

/** Whether a key press asks for undo or redo: ⌘Z and ⌘U on a Mac, Ctrl on
 * the rest. ⇧⌘Z works as redo too, since every editor has it. */
export function historyKey(event:KeyboardEvent):'undo'|'redo'|null{
  if(!(event.metaKey||event.ctrlKey)||event.altKey)return null;
  if(event.target instanceof HTMLInputElement||event.target instanceof HTMLTextAreaElement)return null;
  const key=event.key.toLowerCase();
  if(key==='z')return event.shiftKey?'redo':'undo';
  if(key==='u'&&!event.shiftKey)return 'redo';
  return null;
}

/** A page's undo history: what was done, in order, and what was undone and
 * can be done again. A new action after an undo drops the redo side, as in
 * any editor, and only the last hundred actions are kept. */
export function useHistory(){
  const past=useRef<HistoryEntry[]>([]);
  const future=useRef<HistoryEntry[]>([]);
  const [counts,setCounts]=useState({past:0,future:0});
  const sync=()=>setCounts({past:past.current.length,future:future.current.length});
  const record=useCallback((entry:HistoryEntry)=>{
    past.current=[...past.current.slice(-(HISTORY_LIMIT-1)),entry];
    future.current=[];
    sync();
  },[]);
  /** Takes the last action off the stack for undoing; `keep` puts it back
   * when the undo could not be applied. */
  const takeUndo=useCallback(()=>{
    const entry=past.current.pop();
    if(entry)future.current.push(entry);
    sync();
    return entry?{entry,keep:()=>{future.current.pop();past.current.push(entry);sync()}}:null;
  },[]);
  const takeRedo=useCallback(()=>{
    const entry=future.current.pop();
    if(entry)past.current.push(entry);
    sync();
    return entry?{entry,keep:()=>{past.current.pop();future.current.push(entry);sync()}}:null;
  },[]);
  return {record,takeUndo,takeRedo,canUndo:counts.past>0,canRedo:counts.future>0};
}

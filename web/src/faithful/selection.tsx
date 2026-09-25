import {createContext,useContext,useEffect,useLayoutEffect,useRef,useState,type ReactNode} from 'react';
import {Icon,type IconName} from '../Icon';

// Multi-select as in Google Photos. While anything is selected the top bar
// turns into the selection bar: a close button and the count on the left, and
// the page's actions as icons on the right, where they stay however far the
// grid is scrolled. Each page owns its selection; the frame only draws it.

export type SelectionAction={label:string;icon:IconName;onClick:()=>void;disabled?:boolean;danger?:boolean};
export type Selection={count:number;clear:()=>void;actions:SelectionAction[];busy?:boolean};

const SelectionContext=createContext<(selection:Selection|null)=>void>(()=>{});
export const SelectionProvider=SelectionContext.Provider;

/** Puts the page's selection in the top bar. The bar is redrawn only when what
 * it shows changes; the latest callbacks are always the ones called. */
export function useSelectionBar(selection:Selection|null){
  const set=useContext(SelectionContext);
  const latest=useRef(selection);
  latest.current=selection;
  const signature=selection&&selection.count>0
    ?`${selection.count}|${selection.busy?1:0}|${selection.actions.map(action=>`${action.label}:${action.disabled?0:1}`).join(',')}`
    :'';
  useLayoutEffect(()=>{
    const current=latest.current;
    if(!current||current.count===0){set(null);return}
    set({
      count:current.count,busy:current.busy,
      clear:()=>latest.current?.clear(),
      actions:current.actions.map((action,index)=>({...action,onClick:()=>latest.current?.actions[index]?.onClick()})),
    });
  },[signature,set]);
  useEffect(()=>()=>set(null),[set]);
}

export function SelectionBar({selection}:{selection:Selection}){
  useEffect(()=>{
    const close=(event:KeyboardEvent)=>{if(event.key==='Escape'&&!(event.target instanceof HTMLInputElement))selection.clear()};
    window.addEventListener('keydown',close);
    return()=>window.removeEventListener('keydown',close);
  },[selection]);
  return <header className="gbar selecting" role="toolbar" aria-label="Selection">
    <button type="button" className="iconbtn" aria-label="Clear the selection" title="Clear the selection" onClick={selection.clear}><Icon name="close"/></button>
    <span className="selcount" role="status">{selection.count.toLocaleString()} selected</span>
    <div className="gbaracts">
      {selection.actions.map(action=><button key={action.label} type="button" className={`iconbtn${action.danger?' danger':''}`}
        aria-label={action.label} title={action.label} disabled={selection.busy||action.disabled} onClick={action.onClick}><Icon name={action.icon}/></button>)}
    </div>
  </header>;
}

/** The round tick in a tile's top-left corner. It shows on hover, stays once
 * something is selected, and fills in for the tiles that are. */
export function Pick({checked,label,onToggle}:{checked:boolean;label:string;onToggle:(extend:boolean)=>void}){
  return <button type="button" role="checkbox" aria-checked={checked} aria-label={label} className="pick"
    onClick={event=>{event.stopPropagation();onToggle(event.shiftKey)}}>
    <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="10"/><path d="m7.5 12.2 3 3 6-6.4"/></svg>
  </button>;
}

/** Tracks a set of selected ids with shift-click runs, the way Google Photos
 * does: a shift-click selects or clears everything between it and the last
 * tile clicked. */
export function usePicks<T,K extends string|number=number>(items:T[],id:(item:T)=>K){
  const [picked,setPicked]=useState<Set<K>>(new Set());
  const anchor=useRef<number|null>(null);
  function toggle(index:number,extend:boolean){
    const key=id(items[index]);
    setPicked(previous=>{
      const next=new Set(previous);
      if(extend&&anchor.current!==null){
        const start=Math.min(anchor.current,index),end=Math.max(anchor.current,index),adding=!next.has(key);
        for(let i=start;i<=end;i++){if(adding)next.add(id(items[i]));else next.delete(id(items[i]))}
        return next;
      }
      if(next.has(key))next.delete(key);else next.add(key);
      return next;
    });
    anchor.current=index;
  }
  function clear(){setPicked(new Set());anchor.current=null}
  function all(){setPicked(new Set(items.map(id)));anchor.current=null}
  // A selection only ever names what the list still holds.
  const present=new Set(items.map(id));
  const stale=[...picked].some(key=>!present.has(key));
  useEffect(()=>{if(stale)setPicked(previous=>new Set([...previous].filter(key=>present.has(key))))});
  return {picked,toggle,clear,all,setPicked};
}

/** A row-filling grid, as in Google Photos: every tile keeps its picture's
 * shape, rows share one height and run edge to edge, and the last row is left
 * short rather than stretched. A tile learns its shape from its own picture
 * once it loads, so nothing has to be known in advance. */
export function Rows({children,className=''}:{children:ReactNode;className?:string}){
  return <div className={`rows ${className}`}>{children}</div>;
}

export function RowTile({ratio:known,className='',children,...rest}:{ratio?:number;className?:string;children:ReactNode}&Omit<React.HTMLAttributes<HTMLElement>,'className'|'children'>){
  const [ratio,setRatio]=useState(known&&known>0?known:1);
  const ref=useRef<HTMLElement>(null);
  useEffect(()=>{
    const element=ref.current;
    if(!element)return;
    const measure=(event:Event)=>{
      const target=event.target;
      if(target instanceof HTMLImageElement&&target.naturalWidth>0&&target.naturalHeight>0)setRatio(target.naturalWidth/target.naturalHeight);
    };
    // A picture already loaded from the cache fires no event.
    const ready=element.querySelector('img');
    if(ready&&ready.complete&&ready.naturalWidth>0)setRatio(ready.naturalWidth/ready.naturalHeight);
    element.addEventListener('load',measure,true);
    return()=>element.removeEventListener('load',measure,true);
  },[]);
  const shape=Math.min(3,Math.max(.4,ratio));
  return <figure ref={ref} className={`rt ${className}`} style={{'--r':shape} as React.CSSProperties} {...rest}>{children}</figure>;
}

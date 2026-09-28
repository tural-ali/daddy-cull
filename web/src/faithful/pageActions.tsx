import {createContext,useContext,useEffect,useLayoutEffect,useRef} from 'react';
import {Busy} from '../Busy';
import {Icon,type IconName} from '../Icon';
import {covered,matches,tipProps,typing} from './keys';

// A page's own actions sit at the right of the top bar, as Google Photos puts
// Empty trash or Share there, so they are in the same place on every page and
// stay in reach however far the page is scrolled. The page says what they are;
// the frame draws them.

/** On a phone the top bar has little room, so an action shows its `icon`
 * there, or failing that its `short` label. Every action has `keys`, shown in
 * its tooltip, and `busy` puts the orb in the button while it works. */
export type PageAction={label:string;short?:string;icon?:IconName;keys:string;onClick:()=>void;disabled?:boolean;busy?:boolean;primary?:boolean;title?:string};
export type PageActions={note?:string;actions:PageAction[]};

const PageActionsContext=createContext<(actions:PageActions|null)=>void>(()=>{});
export const PageActionsProvider=PageActionsContext.Provider;

/** Puts the page's actions in the top bar, or takes them away with null. The
 * bar is redrawn only when what it shows changes; the latest callbacks are
 * always the ones called. */
export function usePageActions(page:PageActions|null){
  const set=useContext(PageActionsContext);
  const latest=useRef(page);
  latest.current=page;
  const signature=page&&page.actions.length>0
    ?`${page.note??''}|${page.actions.map(action=>`${action.label}:${action.short??''}:${action.icon??''}:${action.keys}:${action.disabled?0:1}:${action.busy?1:0}:${action.primary?1:0}:${action.title??''}`).join(',')}`
    :'';
  useLayoutEffect(()=>{
    const current=latest.current;
    if(!current||current.actions.length===0){set(null);return}
    set({note:current.note,actions:current.actions.map((action,index)=>({...action,onClick:()=>latest.current?.actions[index]?.onClick()}))});
  },[signature,set]);
  useEffect(()=>()=>set(null),[set]);
}

export function PageActionButtons({page}:{page:PageActions}){
  const latest=useRef(page);
  useLayoutEffect(()=>{latest.current=page});
  useEffect(()=>{
    const key=(event:KeyboardEvent)=>{
      if(event.defaultPrevented||event.repeat||typing(event.target)||covered())return;
      const action=latest.current.actions.find(one=>matches(event,one.keys));
      if(!action)return;
      event.preventDefault();
      if(!action.disabled&&!action.busy)action.onClick();
    };
    window.addEventListener('keydown',key);
    return()=>window.removeEventListener('keydown',key);
  },[]);
  return <div className="pageacts">
    {page.note&&<span className="pagenote" role="status">{page.note}</span>}
    {page.actions.map(action=><button key={action.label} type="button" className={`${action.primary?'btn primary':'textbtn'}${action.icon?' hasicon':''}`}
      {...tipProps(action.title??action.label,action.keys)} aria-label={action.short||action.icon?action.label:undefined} aria-busy={action.busy||undefined} disabled={action.disabled||action.busy} onClick={action.onClick}>
      {action.busy?<Busy label="" state="working"/>:action.icon&&<Icon name={action.icon}/>}<span className="long">{action.label}</span>{action.short&&<span className="short">{action.short}</span>}</button>)}
  </div>;
}

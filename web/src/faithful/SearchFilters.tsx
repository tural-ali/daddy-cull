import {createContext,useContext,useEffect,useLayoutEffect,useRef,type RefObject} from 'react';
import {Icon,type IconName} from '../Icon';
import {usePopover} from './CalendarPopover';

// A page that can narrow what it shows puts its filters in the search bar,
// beside its date, the way Google Photos turns a search into chips: each
// filter that is on is a pill that removes itself, the filter button lists
// them all with their counts, and typing a filter's name offers it.

/** One way to narrow the page. Filters in the same `group` widen each other
 * (videos or photos); filters in different groups combine. `words` are what
 * typing in the search field matches, the label's own word first. */
export type FilterOption={id:string;label:string;icon:IconName;count:number;on:boolean;group:string;words:string[]};
export type PageFilters={options:FilterOption[];toggle:(id:string)=>void;clear:()=>void};

const PageFiltersContext=createContext<(filters:PageFilters|null)=>void>(()=>{});
export const PageFiltersProvider=PageFiltersContext.Provider;

/** Puts the page's filters in the search bar, or takes them away with null. */
export function usePageFilters(filters:PageFilters|null){
  const set=useContext(PageFiltersContext);
  const latest=useRef(filters);
  latest.current=filters;
  const signature=filters?JSON.stringify(filters.options):'';
  useLayoutEffect(()=>{
    const current=latest.current;
    // The bar calls through the ref, so it always reaches the page's latest
    // handlers without being told about every render.
    set(current?{options:current.options,toggle:id=>latest.current?.toggle(id),clear:()=>latest.current?.clear()}:null);
  },[signature,set]);
  useEffect(()=>()=>set(null),[set]);
}

/** The filters a typed word could mean: two letters or more, matched against
 * the start of any of a filter's words. */
export function matchFilters(options:FilterOption[],text:string){
  const typed=text.trim().toLowerCase();
  if(typed.length<2)return [];
  return options.filter(option=>option.words.some(word=>word.startsWith(typed)));
}

/** The filters that are on, as pills that each take their own filter off. */
export function FilterPills({filters}:{filters:PageFilters}){
  const on=filters.options.filter(option=>option.on);
  if(on.length===0)return null;
  return <span className="filterpills">
    {on.map(option=><button key={option.id} type="button" className="filterpill" aria-label={`Remove the ${option.label} filter`} title={`Remove the ${option.label} filter`} onClick={()=>filters.toggle(option.id)}>
      <Icon name={option.icon} filled/><span className="label">{option.label}</span><Icon name="close"/>
    </button>)}
  </span>;
}

export function FilterButton({filters,button,open,onToggle}:{filters:PageFilters;button:RefObject<HTMLButtonElement|null>;open:boolean;onToggle:()=>void}){
  const on=filters.options.filter(option=>option.on).length;
  const label=on?`Filters, ${on} on`:'Filters';
  return <button ref={button} type="button" className={`filterbtn${on?' on':''}`} aria-haspopup="dialog" aria-expanded={open} aria-label={label} title="Show only" onClick={onToggle}>
    <Icon name="filter_list"/>{on>0&&<span className="n" aria-hidden="true">{on}</span>}
  </button>;
}

/** Every filter with its count, in its group, each a switch. */
export function FilterMenu({filters,anchor,onClose}:{filters:PageFilters;anchor:RefObject<HTMLButtonElement|null>;onClose:()=>void}){
  const panel=useRef<HTMLDivElement>(null);
  const keys=usePopover(panel,anchor,onClose);
  useEffect(()=>{panel.current?.querySelector<HTMLElement>('.filteropt')?.focus({preventScroll:true})},[]);
  const groups=[...new Set(filters.options.map(option=>option.group))];
  const on=filters.options.some(option=>option.on);
  return <div ref={panel} className="calpop filtermenu" role="dialog" aria-label="Show only" tabIndex={-1} {...keys}>
    <div className="filterhead"><b>Show only</b>{on&&<button type="button" className="textbtn" onClick={filters.clear}>Clear</button>}</div>
    {groups.map(group=><div key={group} className="filtergroup" role="group" aria-label={group}>
      {filters.options.filter(option=>option.group===group).map(option=><button key={option.id} type="button" className={`filteropt${option.on?' on':''}`} aria-pressed={option.on} onClick={()=>filters.toggle(option.id)}>
        <Icon name={option.icon} filled={option.on}/><span className="label">{option.label}</span>
        <span className="n">{option.count.toLocaleString()}</span>
        <span className="tick"><Icon name="check"/></span>
      </button>)}
    </div>)}
  </div>;
}

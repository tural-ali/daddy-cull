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
/** An order the page can be shown in. The first of a page's orders is its
 * usual one; any other shows as a pill, which puts the usual one back. */
export type SortOption={id:string;label:string;icon:IconName;words:string[]};
export type PageSort={options:SortOption[];value:string;set:(id:string)=>void};
export type PageFilters={options:FilterOption[];toggle:(id:string)=>void;clear:()=>void;sort?:PageSort};

/** What typing can pick: a filter to switch, or an order to show. */
export type Suggestion={kind:'filter';option:FilterOption}|{kind:'sort';option:SortOption;on:boolean};

const PageFiltersContext=createContext<(filters:PageFilters|null)=>void>(()=>{});
export const PageFiltersProvider=PageFiltersContext.Provider;

/** Puts the page's filters in the search bar, or takes them away with null. */
export function usePageFilters(filters:PageFilters|null){
  const set=useContext(PageFiltersContext);
  const latest=useRef(filters);
  latest.current=filters;
  const signature=filters?JSON.stringify([filters.options,filters.sort?.options,filters.sort?.value]):'';
  useLayoutEffect(()=>{
    const current=latest.current;
    // The bar calls through the ref, so it always reaches the page's latest
    // handlers without being told about every render.
    set(current?{options:current.options,toggle:id=>latest.current?.toggle(id),clear:()=>latest.current?.clear(),
      sort:current.sort&&{options:current.sort.options,value:current.sort.value,set:id=>latest.current?.sort?.set(id)}}:null);
  },[signature,set]);
  useEffect(()=>()=>set(null),[set]);
}

/** The filters and orders a typed word could mean: two letters or more,
 * matched against the start of any of their words. The order already shown
 * is not offered. */
export function matchFilters(filters:PageFilters,text:string):Suggestion[]{
  const typed=text.trim().toLowerCase();
  if(typed.length<2)return [];
  const said=(words:string[])=>words.some(word=>word.startsWith(typed));
  const found:Suggestion[]=filters.options.filter(option=>said(option.words)).map(option=>({kind:'filter',option}));
  for(const option of filters.sort?.options??[])if(option.id!==filters.sort?.value&&said(option.words))found.push({kind:'sort',option,on:false});
  return found;
}

/** The order shown, when it is not the page's usual one. */
export function unusualSort(filters:PageFilters){
  const sort=filters.sort;
  if(!sort||sort.value===sort.options[0]?.id)return null;
  return sort.options.find(option=>option.id===sort.value)??null;
}

/** The filters that are on, as pills that each take their own filter off. */
export function FilterPills({filters}:{filters:PageFilters}){
  const on=filters.options.filter(option=>option.on);
  const order=unusualSort(filters);
  if(on.length===0&&!order)return null;
  const usual=filters.sort?.options[0];
  return <span className="filterpills">
    {on.map(option=><button key={option.id} type="button" className="filterpill" aria-label={`Remove the ${option.label} filter`} title={`Remove the ${option.label} filter`} onClick={()=>filters.toggle(option.id)}>
      <Icon name={option.icon} filled/><span className="label">{option.label}</span><Icon name="close"/>
    </button>)}
    {order&&usual&&<button type="button" className="filterpill" aria-label={`Show ${usual.label.toLowerCase()} again`} title={`Show ${usual.label.toLowerCase()} again`} onClick={()=>filters.sort?.set(usual.id)}>
      <Icon name={order.icon} filled/><span className="label">{order.label}</span><Icon name="close"/>
    </button>}
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
    {filters.sort&&<div className="filtergroup sortgroup" role="radiogroup" aria-label="Order">
      <p className="filterlabel">Order</p>
      {filters.sort.options.map(option=><button key={option.id} type="button" role="radio" className={`filteropt${option.id===filters.sort?.value?' on':''}`} aria-checked={option.id===filters.sort?.value} onClick={()=>filters.sort?.set(option.id)}>
        <Icon name={option.icon} filled={option.id===filters.sort?.value}/><span className="label">{option.label}</span>
        <span className="tick"><Icon name="check"/></span>
      </button>)}
    </div>}
  </div>;
}

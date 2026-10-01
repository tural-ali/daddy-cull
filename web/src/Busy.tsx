import {LogoMark} from './Logo';
import {ProgressBar} from './ProgressBar';

type BusyState='working'|'searching'|'solving'|'listening'|'connecting'|'weaving'|'composing'|'breathing'|'shaping';

/** One waiting language: the logo's three tiles, a label, and a shared rail
 * for standalone loaders. Inline controls keep their original height. */
export function Busy({label,state='searching',size=20}:{label:string;state?:BusyState;size?:20|64}){
  return <span className={size===64?'busy big':'busy'} role="status" data-state={state}>
    <LogoMark className="busy-mark"/>
    <span className="busy-label">{label}</span>
    {size===64&&<ProgressBar label={label} decorative/>}
  </span>;
}

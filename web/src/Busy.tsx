export function Busy({label,state='searching',size=20}:{label:string;state?:'working'|'searching'|'solving'|'listening'|'connecting'|'weaving'|'composing'|'breathing'|'shaping';size?:20|64}){
  return <span className="busy" role="status" data-state={state} data-size={size}>{label}</span>;
}

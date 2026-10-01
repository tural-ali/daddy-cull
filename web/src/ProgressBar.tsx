/** Shared determinate and indeterminate progress. Unknown totals never show
 * a fabricated percentage; decorative rails defer announcements to a label. */
export function ProgressBar({label,value,max=100,className='',decorative=false}:{label:string;value?:number;max?:number;className?:string;decorative?:boolean}){
  const known=value!==undefined&&Number.isFinite(value)&&Number.isFinite(max)&&max>0;
  const amount=known?Math.max(0,Math.min(max,value)):undefined;
  return <span className={`progress-track${known?'':' indeterminate'}${className?` ${className}`:''}`}
    role={decorative?undefined:'progressbar'} aria-hidden={decorative||undefined}
    aria-label={decorative?undefined:label} aria-valuemin={decorative?undefined:0}
    aria-valuemax={decorative?undefined:known?max:100} aria-valuenow={decorative?undefined:amount}>
    <span className="progress-fill" style={known?{width:`${amount!/max*100}%`}:undefined}/>
  </span>;
}

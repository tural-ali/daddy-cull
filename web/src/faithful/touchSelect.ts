// A finger selects a photo the way Photos on an iPad does: by pressing and
// holding it. The round tick in a tile's corner shows on hover, which a
// finger never gives, so until something is selected it is left out of reach
// (see .pick in the stylesheet) and a hold on the tile presses it instead.
// Once anything is selected the ticks show and a tap selects, as it does with
// a mouse. Every grid with ticks gets this from one listener on the document.

const holdTime=450;
const slop=10;

export function startTouchSelect(){
  let timer=0;
  let start:{x:number;y:number}|null=null;
  // The tile whose hold just selected it: the click its lift sends is not a
  // tap that opens it.
  let held:Element|null=null;
  const cancel=()=>{window.clearTimeout(timer);start=null};
  document.addEventListener('pointerdown',event=>{
    cancel();
    if(event.pointerType!=='touch'||!event.isPrimary)return;
    const tile=(event.target as Element).closest?.(':is(.rt, .mo):has(> .pick)');
    const pick=tile?.querySelector<HTMLButtonElement>(':scope > .pick');
    if(!tile||!pick||(event.target as Element).closest('button,a,input,select'))return;
    start={x:event.clientX,y:event.clientY};
    // Marked held only after the press, which would otherwise be swallowed
    // as the lift's click.
    timer=window.setTimeout(()=>{start=null;pick.click();held=tile},holdTime);
  },{passive:true});
  document.addEventListener('pointermove',event=>{
    if(start&&Math.hypot(event.clientX-start.x,event.clientY-start.y)>slop)cancel();
  },{passive:true});
  document.addEventListener('pointerup',cancel,{passive:true});
  document.addEventListener('pointercancel',cancel,{passive:true});
  document.addEventListener('click',event=>{
    if(!held)return;
    const tile=held;held=null;
    if(tile.contains(event.target as Node)){event.preventDefault();event.stopPropagation()}
  },{capture:true});
  // A hold that ends off the tile sends no click to swallow.
  document.addEventListener('pointerdown',()=>{held=null},{capture:true,passive:true});
}

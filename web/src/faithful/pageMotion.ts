// How one page gives way to the next. The page left fades while it can no
// longer be pressed, and the next rises into its place, so a move feels like
// turning to another part of the same place rather than a fresh load. A date
// pressed in the year opens out of its own square: the square grows to fill
// the panel, its colour melting into the page, and the day's photographs come
// in after it. Going from a day back to the year zooms out onto that day's
// square. Anyone who has asked for less motion gets the pages swapped at
// once, as before.

/** How long the page left takes to go, and how long a date's square takes to
 * fill the panel, in milliseconds. The next page waits for whichever applies
 * before it comes in. */
export const LEAVE=140,ZOOM=360;

/** How many screens tall a page can be and still rise in; a longer one
 * fades in where it is. */
export const LONG=4;

/** A date square pressed in the year, as it was on screen. */
export type Zoom={href:string;rect:DOMRect;colour:string;ink:string;size:string;label:string};

let pressed:Zoom|null=null;

/** Remembers the square pressed, for the move it starts to open out of. */
export function zoomFrom(cell:HTMLElement){
  const style=getComputedStyle(cell);
  pressed={
    href:cell.getAttribute('href')??'',
    rect:cell.getBoundingClientRect(),
    colour:style.backgroundColor,
    ink:style.color,
    size:style.fontSize,
    label:cell.firstChild?.textContent?.trim()??'',
  };
}

/** The square pressed for a move to path, once; any other move forgets it. */
export function takeZoom(path:string):Zoom|null{
  const zoom=pressed;
  pressed=null;
  if(!zoom)return null;
  return new URL(zoom.href,location.href).pathname===path?zoom:null;
}

/** Whether the viewer has asked for less motion. */
export function calm(){
  return matchMedia('(prefers-reduced-motion: reduce)').matches;
}

/** The part of the panel on screen, which the square grows to fill. */
export function panelRect(){
  const panel=document.querySelector('.panel');
  const rect=panel?.getBoundingClientRect();
  if(!rect)return new DOMRect(0,0,innerWidth,innerHeight);
  const top=Math.max(rect.top,0),bottom=Math.min(rect.bottom,innerHeight);
  return new DOMRect(rect.left,top,rect.width,Math.max(bottom-top,0));
}

/** Grows the pressed square into the panel, in a layer of its own above the
 * page and below the top bar. It stays, the panel's colour, until fade is
 * called once the day is drawn. */
export function growSquare(zoom:Zoom){
  const to=panelRect(),from=zoom.rect;
  const tile=document.createElement('div');
  tile.className='zoomtile';
  tile.setAttribute('aria-hidden','true');
  const label=document.createElement('span');
  label.textContent=zoom.label;
  tile.append(label);
  document.body.append(tile);
  const box=(rect:DOMRect)=>({left:`${rect.left}px`,top:`${rect.top}px`,width:`${rect.width}px`,height:`${rect.height}px`});
  const bg=getComputedStyle(document.documentElement).getPropertyValue('--bg').trim()||'#fff';
  const ease='cubic-bezier(.3,.72,.12,1)';
  tile.animate([
    {...box(from),borderRadius:'8px',backgroundColor:zoom.colour,boxShadow:'0 4px 14px rgb(0 0 0 / 25%)'},
    {...box(to),borderRadius:'24px 24px 0 0',backgroundColor:bg,boxShadow:'0 0 0 rgb(0 0 0 / 0%)'},
  ],{duration:ZOOM,easing:ease,fill:'forwards'});
  // The date itself grows with its square and fades into the page.
  label.animate([
    {fontSize:zoom.size,color:zoom.ink,opacity:1},
    {fontSize:'min(28vw, 220px)',color:zoom.ink,opacity:0.14},
  ],{duration:ZOOM,easing:ease,fill:'forwards'});
  return{
    fade(){
      const gone=tile.animate([{opacity:1},{opacity:0}],{duration:240,easing:'ease-out',fill:'forwards'});
      gone.onfinish=()=>tile.remove();
    },
    remove(){tile.remove()},
  };
}

/** The photographs of a day come in one after another, nearest the top
 * first; only the first few dozen wait in turn, so a long day is not slow to
 * show. A grid measures its width before it places any photograph, so they
 * are numbered a frame later, once they are there; each has begun to wait by
 * then, and takes its turn from the number. Returns a cancel. */
export function staggerGrid(stage:HTMLElement){
  const frame=requestAnimationFrame(()=>{
    const figures=stage.querySelectorAll<HTMLElement>('.jgrid figure');
    figures.forEach((figure,index)=>{if(index<32)figure.style.setProperty('--i',String(index))});
  });
  return()=>cancelAnimationFrame(frame);
}

/** Where the square of a day sits within the page drawn in stage, for the
 * year to zoom out onto it, as a transform origin. */
export function squareOrigin(stage:HTMLElement,md:string){
  const cell=stage.querySelector(`.cell[href="/on/${md}"]`);
  if(!cell)return null;
  const box=stage.getBoundingClientRect(),rect=cell.getBoundingClientRect();
  return `${rect.left-box.left+rect.width/2}px ${rect.top-box.top+rect.height/2}px`;
}

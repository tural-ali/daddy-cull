import {useEffect,useLayoutEffect,useMemo,useRef,useState,type CSSProperties,type ReactNode} from 'react';

// Photos in rows at their own shapes, as Google Photos lays out a library:
// each row is as tall as it needs to be for its pictures to fill the width
// exactly, and a row ends wherever that keeps its height closest to the
// target, so no picture is cropped and no row is far off the rest. The last
// row is not stretched to fill. The target is the grid's --row, which the
// thumbnail zoom scales, so a hidden ruler is measured rather than the
// number being kept in two places.

/** One tile's shape, width over height, if known before it loads, and the
 * quarter turns it is shown at, which swap a picture's sides when odd. */
export type JustifiedItem={key:string|number;ratio?:number;turn?:number};
export type Box={width:number;height:number};

/** A picture wider than this, or narrower than its inverse, is shown cropped
 * to it, so a panorama cannot take a row to itself at a sliver's height. */
const widest=3;
/** A row with too few pictures to fill it, a lone portrait on a phone say,
 * stops growing at this many times the target and is left short. */
const tallest=1.5;

function clamp(ratio:number){return Math.min(widest,Math.max(1/widest,ratio))}

type Row={height:number;tiles:{index:number;width:number}[]};

let lastSize={width:0,target:0,gap:0};

/** Breaks the shapes into rows for a grid `width` wide with `gap` between
 * tiles, each row near `target` tall. */
function layout(ratios:number[],width:number,target:number,gap:number):Row[]{
  const rows:Row[]=[];
  if(width<=0||target<=0)return [];
  const heightOf=(sum:number,count:number)=>(width-gap*(count-1))/sum;
  let start=0,sum=0;
  const close=(end:number,height:number)=>{
    const shown=Math.min(height,target*tallest);
    const tiles=[];
    let used=0;
    for(let index=start;index<end;index++){
      const last=index===end-1;
      // The last tile of a full row takes up the rounding, so the row ends
      // exactly on the grid's edge.
      const exact=last&&shown===height?width-used-gap*(end-start-1):Math.round(ratios[index]*shown);
      tiles.push({index,width:Math.max(1,exact)});
      used+=Math.round(ratios[index]*shown);
    }
    rows.push({height:Math.round(shown),tiles});
    start=end;sum=0;
  };
  for(let index=0;index<ratios.length;index++){
    const before=sum>0?heightOf(sum,index-start):Infinity;
    sum+=ratios[index];
    const after=heightOf(sum,index-start+1);
    if(after>target)continue;
    // Adding this tile took the row below the target: end the row either
    // side of it, whichever is nearer.
    if(index>start&&before-target<target-after){
      close(index,before);
      sum=ratios[index];
      if(heightOf(sum,1)<=target)close(index+1,heightOf(sum,1));
    }else close(index+1,after);
  }
  if(start<ratios.length){
    // The last row keeps the target height rather than being stretched.
    const end=ratios.length;
    const rest=heightOf(sum,end-start);
    const height=Math.min(target,rest);
    const tiles=[];
    for(let index=start;index<end;index++)tiles.push({index,width:Math.max(1,Math.round(ratios[index]*height))});
    rows.push({height:Math.round(height),tiles});
  }
  return rows;
}

/** A justified grid. `render` draws tile `index` at the box it is given; a
 * tile whose shape was not known is measured from its picture once it loads,
 * and the grid settles around it. */
export function Justified({items,className='',render}:{items:JustifiedItem[];className?:string;render:(index:number,box:Box)=>ReactNode}){
  const grid=useRef<HTMLDivElement>(null);
  const ruler=useRef<HTMLSpanElement>(null);
  // A grid drawn again, as a page refreshed in place is, starts at the size
  // the last one had, so the page is as tall as before on its first frame and
  // keeps its place instead of scrolling to the top while it measures.
  const [size,setSize]=useState(()=>lastSize);
  // Shapes read from the pictures themselves, by key.
  const [measured,setMeasured]=useState<ReadonlyMap<string,number>>(()=>new Map());
  useLayoutEffect(()=>{
    const node=grid.current,mark=ruler.current;
    if(!node||!mark)return;
    const read=()=>{
      const width=node.clientWidth;
      const target=mark.offsetHeight;
      const gap=Number.parseFloat(getComputedStyle(node).columnGap)||0;
      lastSize={width,target,gap};
      setSize(current=>current.width===width&&current.target===target&&current.gap===gap?current:{width,target,gap});
    };
    read();
    const observer=new ResizeObserver(read);
    observer.observe(node);
    observer.observe(mark);
    return()=>observer.disconnect();
  },[]);
  // A picture that loads with no shape known gives its own; the changes of
  // one frame are taken together, so a page of tiles settles once.
  useEffect(()=>{
    const node=grid.current;
    if(!node)return;
    let pending=new Map<string,number>();
    let frame=0;
    const loaded=(event:Event)=>{
      const target=event.target;
      if(!(target instanceof HTMLImageElement)||!target.naturalWidth||!target.naturalHeight)return;
      const tile=target.closest<HTMLElement>('[data-shape]');
      if(tile?.dataset.shape!=='unknown')return;
      pending.set(tile.dataset.key!,target.naturalWidth/target.naturalHeight);
      if(!frame)frame=requestAnimationFrame(()=>{
        const found=pending;
        pending=new Map();frame=0;
        setMeasured(current=>new Map([...current,...found]));
      });
    };
    node.addEventListener('load',loaded,true);
    return()=>{node.removeEventListener('load',loaded,true);cancelAnimationFrame(frame)};
  },[]);
  const ratios=useMemo(()=>items.map(item=>{
    const own=item.ratio&&item.ratio>0?item.ratio:measured.get(String(item.key))??1;
    return clamp((item.turn??0)%2?1/own:own);
  }),[items,measured]);
  const rows=useMemo(()=>layout(ratios,size.width,size.target,size.gap),[ratios,size]);
  return <div ref={grid} className={`jgrid ${className}`}>
    <span ref={ruler} className="jruler" aria-hidden="true"/>
    {rows.map(row=><div className="jrow" key={items[row.tiles[0].index].key} style={{height:row.height}}>
      {row.tiles.map(tile=>render(tile.index,{width:tile.width,height:row.height}))}
    </div>)}
  </div>;
}

/** A file's shape, width over height, when the server has read it. */
export function shapeOf(asset:{width?:number;height?:number}){
  return asset.width&&asset.height?asset.width/asset.height:undefined;
}

/** The attributes a tile carries so the grid can measure its picture, and
 * turn it: a turned picture is drawn at its own shape and turned into the
 * tile's, which needs the tile's size. */
export function shapeProps(item:JustifiedItem,box:Box){
  const turn=item.turn??0;
  return {'data-key':String(item.key),'data-shape':item.ratio&&item.ratio>0?'known':'unknown',...(turn?{'data-turn':turn}:{}),
    style:{width:box.width,height:box.height,...(turn?{'--tile-w':`${box.width}px`,'--tile-h':`${box.height}px`}:{})} as CSSProperties};
}

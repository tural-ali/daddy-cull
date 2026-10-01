import {useEffect,useId,useState,type CSSProperties} from 'react';
import {wordmark} from './wordmark';

// The Daddy, Cull! mark and wordmark, from the brand SVGs. Three day tiles, coral
// to amber to mint, are the calendar's own scale: much still to cull, some, done.
// The lettering takes its colours from the theme, so one drawing serves day and night.

export const logoTilePoints=[
  [[52,0],[66,14],[28,52],[66,90],[52,104],[0,52]],
  [[91,0],[105,14],[67,52],[105,90],[91,104],[39,52]],
  [[132,0],[184,52],[132,104],[80,52]],
];
const tiles=logoTilePoints.map(points=>`M${points.map(point=>point.join(' ')).join('L')}Z`);

/** The day tiles alone, for small places such as the tab icon. */
export function LogoMark({className}:{className?:string}){
  return <svg className={className} viewBox="-4 -4 192 112" aria-hidden="true" focusable="false">{tiles.map((d,index)=><path key={index} className={`tile t${index}`} d={d}/>)}</svg>;
}

// The intro runs once a page load: 10 s of the whole logo with light passing
// over it, then 900 ms for the wordmark to go. It keeps time from when the
// logo first showed, so a bar drawn again part way through carries on.
const INTRO=10900;
let introStart:number|undefined;
function useIntro(on:boolean){
  const [at,setAt]=useState(()=>{
    if(!on)return undefined;
    introStart??=performance.now();
    const elapsed=performance.now()-introStart;
    return elapsed<INTRO?elapsed:undefined;
  });
  useEffect(()=>{
    if(at===undefined)return;
    const timer=setTimeout(()=>setAt(undefined),INTRO-at);
    return()=>clearTimeout(timer);
  },[at]);
  return {playing:at!==undefined,at:at??0,folded:on&&at===undefined};
}

/** The mark and the wordmark, as the product's name. With `intro` it plays
 * the page-load intro: light runs over the whole of it, as a torch rolled
 * across a metal plate, then the wordmark slides left behind the tiles and
 * only the mark stays. With `shine` it carries the light alone, for whoever
 * draws it to run as it likes. */
export function Logo({className,intro=false,shine=false}:{className?:string;intro?:boolean;shine?:boolean}){
  const {playing,at,folded}=useIntro(intro);
  const lit=playing||shine;
  const id=`logo${useId().replace(/[^\w-]/g,'')}`;
  const art=`${id}art`;
  return <svg className={[className,playing&&'intro',folded&&'folded'].filter(Boolean).join(' ')||undefined} viewBox="64 206 2016 308" preserveAspectRatio="xMinYMid slice"
    role="img" aria-label="Daddy, Cull!" focusable="false" style={playing?{'--intro-at':`${-Math.round(at)}ms`} as CSSProperties:undefined}>
    <defs>
      {/* The wordmark is seen only to the right of the tiles, so sliding
          left it goes in behind them. */}
      <clipPath id={`${id}word`}><rect x="570" y="206" width="1510" height="308"/></clipPath>
      {lit&&<>
        {/* Polished metal: a bright core with the plate darker either side
            of it, so the light shows on white lettering as well as dark. */}
        <linearGradient id={`${id}shine`}>
          <stop offset="0" stopColor="#000" stopOpacity="0"/>
          <stop offset=".28" stopColor="#000" stopOpacity=".05"/>
          <stop offset=".41" stopColor="#000" stopOpacity=".26"/>
          <stop offset=".47" stopColor="#fff" stopOpacity=".55"/>
          <stop offset=".5" stopColor="#fff" stopOpacity=".95"/>
          <stop offset=".53" stopColor="#fff" stopOpacity=".55"/>
          <stop offset=".59" stopColor="#000" stopOpacity=".26"/>
          <stop offset=".72" stopColor="#000" stopOpacity=".05"/>
          <stop offset="1" stopColor="#000" stopOpacity="0"/>
        </linearGradient>
        <mask id={`${id}mask`} maskUnits="userSpaceOnUse" x="64" y="206" width="2016" height="308" style={{maskType:'alpha'}}><use href={`#${art}`}/></mask>
      </>}
    </defs>
    <g id={art}>
      <g transform="translate(78 218) scale(2.595)">{tiles.map((d,index)=><path key={index} className={`tile t${index}`} d={d}/>)}</g>
      <g clipPath={`url(#${id}word)`}><g className="word">
        <path className="ink" fillRule="evenodd" d={wordmark[0]}/>
        <path className="mint" fillRule="evenodd" d={wordmark[1]}/>
      </g></g>
    </g>
    {/* The light, only where the logo is. */}
    {lit&&<g mask={`url(#${id}mask)`}><rect className="shine" x="0" y="150" width="640" height="420" fill={`url(#${id}shine)`}/></g>}
  </svg>;
}

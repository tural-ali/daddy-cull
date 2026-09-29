import type {CSSProperties} from 'react';
import {Icon} from '../Icon';
import {tipProps} from './keys';
import type {LibraryTotals} from './Settings';

/** A size the way Finder writes one, in powers of a thousand: "624 GB",
 * "3.0 TB". */
export function librarySize(bytes:number){
  const units=['bytes','KB','MB','GB','TB','PB'];
  let value=bytes,unit=0;
  while(value>=1000&&unit<units.length-1){value/=1000;unit++}
  if(unit===0)return `${value} bytes`;
  return `${value.toFixed(value<10?1:0)} ${units[unit]}`;
}

function count(files:number,word:string){return `${files.toLocaleString()} ${word}${files===1?'':'s'}`}
const share=(part:number,whole:number)=>whole>0?part/whole:0;
const percent=(value:number)=>`${Math.round(value*100)}%`;

/** One bar of the chart: how a whole splits into photos and videos. */
function Split({label,photos,videos}:{label:string;photos:number;videos:number}){
  const whole=photos+videos,part=share(photos,whole);
  return <div className="libsplit">
    <span className="libsplitlabel">{label}</span>
    <span className="libbar" role="img" aria-label={`${label}: ${percent(part)} photos, ${percent(whole>0?1-part:0)} videos`}>
      {photos>0&&<span className="photos" style={{flexGrow:photos}}/>}
      {videos>0&&<span className="videos" style={{flexGrow:videos}}/>}
    </span>
  </div>;
}

/** What the library holds, under the review meter: how many photos and videos
 * there are and how much space each takes, with a bar for each so it shows at
 * a glance that videos are a third of the files but most of the space.
 * Folded to the rail it is an icon in a ring split the same way by space. */
export function SideLibrary({library,rail}:{library:LibraryTotals;rail:boolean}){
  const {photos,videos}=library;
  const files=photos.files+videos.files,bytes=photos.bytes+videos.bytes;
  const summary=`${count(photos.files,'photo')}, ${librarySize(photos.bytes)}. ${count(videos.files,'video')}, ${librarySize(videos.bytes)}.`;
  return <section className="sidelibrary" aria-labelledby="sidelibrary-title" style={{'--photos':share(photos.bytes,bytes)} as CSSProperties}
    {...(rail?{...tipProps(summary),tabIndex:0}:{})}>
    <h2 className="sidelibraryhead" id="sidelibrary-title"><Icon name="perm_media"/><span className="sidelabel">Library</span></h2>
    <div className="libsplits">
      <Split label="Files" photos={photos.files} videos={videos.files}/>
      <Split label="Space" photos={photos.bytes} videos={videos.bytes}/>
    </div>
    <table className="libkey">
      <thead className="sidelabel"><tr><th scope="col">Kind</th><th scope="col">Files</th><th scope="col">Space</th></tr></thead>
      <tbody>
        <tr><th scope="row"><span className="swatch photos"/>Photos</th><td>{photos.files.toLocaleString()}</td><td>{librarySize(photos.bytes)}</td></tr>
        <tr><th scope="row"><span className="swatch videos"/>Videos</th><td>{videos.files.toLocaleString()}</td><td>{librarySize(videos.bytes)}</td></tr>
      </tbody>
      <tfoot><tr><th scope="row">Total</th><td>{files.toLocaleString()}</td><td>{librarySize(bytes)}</td></tr></tfoot>
    </table>
  </section>;
}

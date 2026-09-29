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

/** What the library holds, under the review meter: how many photos and videos
 * there are and how much space each takes. Folded to the rail it is an icon
 * whose tooltip gives the same numbers. */
export function SideLibrary({library,rail}:{library:LibraryTotals;rail:boolean}){
  const {photos,videos}=library;
  const files=photos.files+videos.files,bytes=photos.bytes+videos.bytes;
  const summary=`${count(photos.files,'photo')}, ${librarySize(photos.bytes)}. ${count(videos.files,'video')}, ${librarySize(videos.bytes)}.`;
  return <section className="sidelibrary" aria-labelledby="sidelibrary-title"
    {...(rail?{...tipProps(summary),tabIndex:0}:{})}>
    <h2 className="sidelibraryhead" id="sidelibrary-title"><Icon name="perm_media"/><span className="sidelabel">Library</span></h2>
    <table className="libkey">
      <thead className="sidelabel"><tr><th scope="col">Kind</th><th scope="col">Files</th><th scope="col">Space</th></tr></thead>
      <tbody>
        <tr><th scope="row">Photos</th><td>{photos.files.toLocaleString()}</td><td>{librarySize(photos.bytes)}</td></tr>
        <tr><th scope="row">Videos</th><td>{videos.files.toLocaleString()}</td><td>{librarySize(videos.bytes)}</td></tr>
      </tbody>
      <tfoot><tr><th scope="row">Total</th><td>{files.toLocaleString()}</td><td>{librarySize(bytes)}</td></tr></tfoot>
    </table>
  </section>;
}

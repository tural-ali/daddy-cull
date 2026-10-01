import {useState} from 'react';
import {Icon} from '../Icon';
import {Lightbox,type LightboxItem} from './Lightbox';
import {Kbd,keyProps,tipProps} from './keys';
import {useShownPath} from './libraryPath';
import type {DuplicateGroup} from './Today';
import {flagsOf} from './Duplicates';

type Member=DuplicateGroup['members'][number];

function bytes(value:number){return value<1024**2?`${(value/1024).toFixed(1)} KB`:value<1024**3?`${(value/1024**2).toFixed(1)} MB`:`${(value/1024**3).toFixed(1)} GB`}
function fileName(path:string){return path.split('/').pop()||path}
function folderOf(path:string){return path.slice(0,path.lastIndexOf('/'))||'/'}
function formatOf(member:Member){
  const name=fileName(member.path);
  const dot=name.lastIndexOf('.');
  return dot>0?name.slice(dot+1).toUpperCase():member.kind.toUpperCase();
}

// A file dated only by its folder is stored at midnight UTC: its day is known
// and its time is not, so the day is read in UTC and no time is shown.
function taken(capturedAt:number){
  if(!capturedAt)return 'Date unknown';
  const dayOnly=capturedAt%86400===0;
  const date=new Date(capturedAt*1000);
  const day=date.toLocaleDateString('en-GB',{day:'numeric',month:'short',year:'numeric',...(dayOnly?{timeZone:'UTC'}:{})});
  return dayOnly?day:`${day}, ${date.toLocaleTimeString('en-GB',{hour:'2-digit',minute:'2-digit'})}`;
}

// How many leading folders every copy shares, so each path can show where it
// stops agreeing with the others: that part is what tells the copies apart.
function sharedDepth(folders:string[][]){
  let depth=0;
  while(folders.every(parts=>depth<parts.length-1&&parts[depth]===folders[0][depth]))depth++;
  return depth;
}

const proofs={bytes:'a full hash of each file',footage:"a full hash of each video's footage",exposure:'the moment and camera each photo records'};

/** The heading over the day's copies, naming how they were proven. */
export function CopiesHeading({groups}:{groups:DuplicateGroup[]}){
  const kinds=[...new Set(groups.map(group=>group.proof))];
  const counted=(noun:string)=>`${groups.length} ${groups.length===1?noun:`${noun}s`}`;
  if(kinds.length===1&&kinds[0]==='bytes')return <h2>Same file, different folders <small>{counted('group')} · byte-identical, verified by full hash</small></h2>;
  if(kinds.length===1&&kinds[0]==='exposure')return <h2>Same shot, two formats <small>{counted('pair')} · a HEIC and a JPEG that record the same moment, to a fraction of a second, and camera</small></h2>;
  const ways=(['bytes','footage','exposure'] as const).filter(kind=>kinds.includes(kind)).map(kind=>proofs[kind]);
  return <h2>Copies <small>{counted('group')} · verified by {ways.slice(0,-1).join(', ')}{ways.length>1?' or ':''}{ways.at(-1)}</small></h2>;
}

/** One group of byte-identical files on the day page: a small preview that
 * opens full size or plays, and a row per copy with everything that tells the
 * copies apart, so the keeper is chosen from facts rather than a thumbnail. */
export function CopyGroup({group,keeperID,saving,onChoose,onResolve}:{group:DuplicateGroup;keeperID:number;saving:boolean;onChoose:(id:number)=>void;onResolve:()=>void}){
  const shown=useShownPath();
  const [open,setOpen]=useState<string|null>(null);
  const [failed,setFailed]=useState(false);
  const keeper=group.members.find(member=>member.id===keeperID)??group.members[0];
  const video=keeper.kind==='video';
  const folders=group.members.map(member=>shown(folderOf(member.path)).split('/'));
  const depth=sharedDepth(folders);
  const items:LightboxItem[]=group.members.map(member=>({key:String(member.id),base:`/api/media/${member.id}`,name:fileName(member.path),kind:member.kind,
    detail:`${formatOf(member)} · ${bytes(member.size)} · ${taken(member.capturedAt)} · ${shown(folderOf(member.path))}`,day:`/day/${member.day}`}));
  const others=group.members.length-1;
  const footage=group.proof==='footage',exposure=group.proof==='exposure';
  return <div className="xgroup xcopy">
    {footage
      ?<p className="xmeta"><span className="b same" title="The pictures and sound are identical, byte for byte, and play the same way. Only the metadata differs, such as dates, names or where it is kept in the file.">Same footage</span>{group.members.length} copies · only the metadata differs · <strong>{bytes(group.reclaimable)}</strong> reclaimable</p>
      :exposure
        ?<p className="xmeta"><span className="b same" title="Both files record the same moment, to a fraction of a second, from the same camera: one shot, saved as HEIC and as JPEG. The JPEG is often the copy made for sharing.">Same shot</span>{group.members.map(formatOf).join(' and ')} · <strong>{bytes(group.reclaimable)}</strong> reclaimable</p>
        :<p className="xmeta">{group.members.length} identical copies · {bytes(group.size)} each · <strong>{bytes(group.reclaimable)}</strong> reclaimable</p>}
    <div className="xbody">
      {/* The copies are byte-identical, hold the same footage, or are one
          shot, so one preview is every copy's. */}
      <button type="button" className="xpeek" aria-label={video?`Play ${fileName(keeper.path)}`:`View ${fileName(keeper.path)} full size`} onClick={()=>setOpen(String(keeper.id))}>
        {failed?<span className="media-missing"><span>No preview</span><small>{formatOf(keeper)}</small></span>
          :<img src={`/api/media/${keeper.id}/preview`} alt="" loading="lazy" decoding="async" onError={()=>setFailed(true)}/>}
        <span className="xpeekicon" aria-hidden="true"><Icon name={video?'play_circle':'zoom_in'} filled={video}/></span>
      </button>
      <div className="xtablewrap">
        <table className="xtable">
          <thead><tr><th scope="col">Keep</th><th scope="col">File</th><th scope="col">Format</th><th scope="col" className="num">Size</th><th scope="col">Taken</th><th scope="col">Folder</th><th scope="col" aria-label="Open"/></tr></thead>
          <tbody>{group.members.map((member,index)=>{
            const chosen=member.id===keeper.id;
            const parts=folders[index];
            const name=fileName(member.path);
            const flags=[...member.path.includes('/.culled/')?['in .culled']:[],...flagsOf(member,group.members,exposure)];
            return <tr key={member.id} data-asset={member.id} className={chosen?'keeper':'binned'} onClick={event=>{if(!(event.target as HTMLElement).closest('button,input')&&!saving)onChoose(member.id)}}>
              <td className="xkeep"><input type="radio" name={`keep-${group.hash}`} checked={chosen} disabled={saving} aria-label={`Keep ${name}`} onChange={()=>onChoose(member.id)}/><span className="xfate">{chosen?'keep':'to Bin'}</span></td>
              <td className="xname" title={name}><span className="xfile">{name}</span>{flags.length>0&&<span className="xflags">{flags.map(flag=><span key={flag} className="dupeflag">{flag}</span>)}</span>}</td>
              <td>{formatOf(member)}</td>
              <td className="num">{bytes(member.size)}</td>
              <td className="xwhen">{taken(member.capturedAt)}</td>
              <td className="xpath" title={shown(member.path)}><span className="dim">{parts.slice(0,depth).map((part,at)=><span key={at} className="xseg">{part}/</span>)}</span>{parts.slice(depth).map((part,at,rest)=><span key={at} className="xseg">{part}{at<rest.length-1?'/':''}</span>)}</td>
              <td className="xopen"><button type="button" className="iconbtn" aria-label={`${member.kind==='video'?'Play':'Open'} ${name}`} {...tipProps(member.kind==='video'?'Play this copy':'Open this copy')} onClick={()=>setOpen(String(member.id))}><Icon name={member.kind==='video'?'play_arrow':'open_in_new'}/></button></td>
            </tr>;
          })}</tbody>
        </table>
      </div>
    </div>
    <p className="xact"><button type="button" className="btn small danger" disabled={saving} onClick={onResolve}>{exposure?`Keep the ${formatOf(keeper)}, mark the ${group.members.filter(member=>member!==keeper).map(formatOf).join(' and ')} for the Bin`:`Keep the selected copy, mark the other ${others===1?'one':others} for the Bin`}</button><span className="hint">Nothing is deleted. The Bin remains separately reviewable and restorable.</span></p>
    {open!==null&&<Lightbox items={items} initialKey={open} onClose={()=>setOpen(null)} renderActions={current=>{
      const id=Number(current.key);
      return id===keeper.id
        ?<span className="xstays"><Icon name="check"/>This copy stays</span>
        :<button type="button" className="rvbtn" disabled={saving} {...keyProps('K')} onClick={()=>onChoose(id)}>Keep this copy instead<Kbd keys="K"/></button>;
    }}/>}
  </div>;
}

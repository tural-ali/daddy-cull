import {Busy} from '../Busy';
import {ProgressBar} from '../ProgressBar';
import {useCallback,useEffect,useRef,useState,type CSSProperties,type RefObject} from 'react';
import {Icon,type IconName} from '../Icon';
import {usePopover} from './CalendarPopover';
import {tipProps,useShortcut} from './keys';
import {active,clearTasks,taskAction,useTasks,type Task} from './taskQueue';

// Beside the bell: what the server is doing in the background. The button
// draws a ring for how far the running tasks have got; opening it lists
// each task with its progress, and lets one be stopped, tried again or
// taken back.

function plural(count:number,word:string){return `${count.toLocaleString()} ${word}${count===1?'':'s'}`}
function size(bytes:number){
  if(bytes<1e6)return `${Math.max(1,Math.round(bytes/1e3))} KB`;
  if(bytes<1e9)return `${(bytes/1e6).toFixed(bytes<1e7?1:0)} MB`;
  return `${(bytes/1e9).toFixed(1)} GB`;
}
function when(stamp:string){
  const at=new Date(stamp);
  if(Number.isNaN(at.getTime()))return '';
  const now=new Date(),time=at.toLocaleTimeString('en-GB',{hour:'2-digit',minute:'2-digit'});
  return at.toDateString()===now.toDateString()?time:`${at.toLocaleDateString('en-GB',{day:'numeric',month:'short'})}, ${time}`;
}

const icons:Record<string,IconName>={
  'screenshots.remove':'delete','screenshots.keep':'drive_file_move','screenshots.restore':'undo',
  'bin.restore':'restore_from_trash','bin.delete':'delete_forever','bin.purge-now':'delete_forever',
};

const verbs:Record<string,string>={
  'screenshots.remove':'moved','screenshots.keep':'copied','screenshots.restore':'brought back',
  'bin.restore':'restored','bin.delete':'deleted','bin.purge-now':'deleted',
};

/** Where a task stands, in words. */
export function progressOf(task:Task){
  const verb=verbs[task.kind]??'done';
  const handled=task.done+task.failed;
  switch(task.state){
    case 'queued':return task.total===1?'Waiting to start':`Waiting to start · ${plural(task.total,'file')}`;
    case 'running':return `${handled.toLocaleString()} of ${task.total.toLocaleString()}`;
    case 'done':return task.bytes>0?`Done · ${size(task.bytes)}`:'Done';
    case 'failed':return `${task.done.toLocaleString()} ${verb}, ${task.failed.toLocaleString()} could not be`;
    case 'cancelled':return task.done>0?`Stopped after ${task.done.toLocaleString()} of ${task.total.toLocaleString()}`:'Stopped before it began';
  }
}

export function TasksButton(){
  const [open,setOpen]=useState(false);
  const button=useRef<HTMLButtonElement>(null);
  const close=useCallback(()=>setOpen(false),[]);
  const list=useTasks();
  const running=(list?.tasks??[]).filter(active);
  const total=running.reduce((sum,task)=>sum+task.total,0);
  const handled=running.reduce((sum,task)=>sum+task.done+task.failed+task.cancelled,0);
  const share=total>0?handled/total:0;
  const failed=(list?.tasks??[]).some(task=>task.state==='failed');
  useShortcut('T',()=>setOpen(current=>!current));
  const label=running.length>0?`Tasks, ${plural(running.length,'task')} running, ${Math.round(share*100)}% done`:'Tasks';
  return <div className="taskwrap">
    <button ref={button} type="button" className={`iconbtn tasks${open?' on':''}${running.length>0?' running':''}`} aria-label={label} {...tipProps('Tasks','T')}
      aria-expanded={open} aria-haspopup="dialog" onClick={()=>setOpen(current=>!current)} style={{'--share':share} as CSSProperties}>
      <Icon name="pending_actions" filled={open}/>
      {running.length>0&&<svg className="taskring" viewBox="0 0 40 40" aria-hidden="true"><circle className="track" cx="20" cy="20" r="18"/><circle className="fill" cx="20" cy="20" r="18" pathLength="100"/></svg>}
      {running.length===0&&failed&&<span className="taskflag" aria-hidden="true"/>}
    </button>
    {open&&<TaskPanel anchor={button} onClose={close}/>}
  </div>;
}

function TaskPanel({anchor,onClose}:{anchor:RefObject<HTMLButtonElement|null>;onClose:()=>void}){
  const list=useTasks();
  const panel=useRef<HTMLDivElement>(null);
  const keys=usePopover(panel,anchor,onClose);
  const [error,setError]=useState('');
  const [working,setWorking]=useState('');
  useEffect(()=>{panel.current?.focus()},[]);
  const tasks=list?.tasks??[];
  const finished=tasks.filter(task=>!active(task)).length;
  async function run(key:string,action:()=>Promise<unknown>){
    setWorking(key);setError('');
    try{await action()}catch(reason){setError((reason as Error).message)}finally{setWorking('')}
  }
  return <div ref={panel} className="calpop notepop taskpop" role="dialog" aria-label="Tasks" tabIndex={-1} {...keys}>
    <div className="notehead taskhead"><h2>Tasks</h2>
      {finished>0&&<button type="button" className="textbtn" disabled={working!==''} onClick={()=>void run('clear',clearTasks)}>Clear finished</button>}
    </div>
    {error&&<p className="notenote warn" role="alert">{error}</p>}
    {list===null?<p className="notenote"><Busy label="Reading tasks…"/></p>
      :tasks.length===0?<p className="notenote">Nothing running. Moving many files to the Bin, deleting or restoring them runs here in the background, so you can carry on.</p>
      :<ul className="notelist tasklist">{tasks.map(task=><TaskItem key={task.id} task={task} working={working} run={run}/>)}</ul>}
  </div>;
}

function TaskItem({task,working,run}:{task:Task;working:string;run:(key:string,action:()=>Promise<unknown>)=>Promise<void>}){
  const handled=task.done+task.failed;
  const leftover=task.failed+task.cancelled;
  const busy=working!=='';
  return <li className={`noteitem taskitem ${task.state}`}>
    <span className={`notetile ${task.kind.startsWith('bin.')&&task.kind!=='bin.restore'?'deletions':task.kind.endsWith('restore')?'arrivals':''}`}><Icon name={icons[task.kind]??'task_alt'}/></span>
    <div className="notebody">
      <p className="notetitle">{task.label}</p>
      <p className="notemeta"><span className="taskstate">{progressOf(task)}</span>{(task.finishedAt||task.createdAt)&&<span>{when(task.finishedAt||task.createdAt)}</span>}</p>
      {active(task)&&<ProgressBar className="meter" label={task.label} value={handled} max={task.total}/>}
      {task.note&&<p className="tasknote">{task.note}</p>}
      {task.keptDays&&!active(task)?<p className="tasknote">They stay on disk for {plural(task.keptDays,'day')}, restorable from the <a href="/log">Log</a>.</p>:null}
      {task.failures.length>0&&<ul className="taskfailures">
        {task.failures.slice(0,3).map((failure,index)=><li key={index}><b>{failure.name}</b> {failure.error}</li>)}
        {task.failed>3&&<li className="dim">and {plural(task.failed-3,'more')}</li>}
      </ul>}
      <p className="noteacts taskacts">
        {active(task)&&<button type="button" className="textbtn" disabled={busy} onClick={()=>void run(task.id,()=>taskAction(task.id,'cancel'))}>Stop</button>}
        {!active(task)&&leftover>0&&<button type="button" className="textbtn" disabled={busy} onClick={()=>void run(task.id,()=>taskAction(task.id,'retry'))}>Try the {plural(leftover,'file')} again</button>}
        {task.undoable&&!active(task)&&<button type="button" className="textbtn" disabled={busy} onClick={()=>void run(task.id,()=>taskAction(task.id,'undo'))}>Undo</button>}
      </p>
    </div>
  </li>;
}

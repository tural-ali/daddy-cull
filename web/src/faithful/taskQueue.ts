import {useSyncExternalStore} from 'react';
import {binChanged,failure} from '../api';

// The task queue: long file operations the server runs in the background, so
// a page that asks for one carries on at once. The list is read here for
// every part of the page: quickly while something is running, rarely when
// nothing is. Progress on any task announces the Bin changed, so the
// sidebar's count follows the files as they move.

export type TaskState='queued'|'running'|'done'|'failed'|'cancelled';
export type TaskFailure={name:string;error:string};
export type Task={id:string;kind:string;label:string;state:TaskState;total:number;done:number;failed:number;cancelled:number;bytes:number;
  keptDays?:number;note?:string;failures:TaskFailure[];createdAt:string;startedAt?:string;finishedAt?:string;undoable:boolean;undoOf?:string};
export type TaskList={tasks:Task[];active:number};

/** How often the list is read while a task is queued or running, and while
 * none is. */
const busyEvery=1000,idleEvery=30000;

let list:TaskList|null=null;
const listeners=new Set<()=>void>();
let timer:ReturnType<typeof setTimeout>|undefined;
let reading:Promise<void>|null=null;
/** Tasks this page queued and has not seen finish. Each is announced when it
 * does, however the list that shows it was read, and the list is read at the
 * busy pace until then. */
const awaited=new Set<string>();

export function active(task:Task){return task.state==='queued'||task.state==='running'}

function publish(next:TaskList){
  const first=list===null,before=new Map((list?.tasks??[]).map(task=>[task.id,task]));
  list=next;
  // A file handled has moved: the Bin, and the counts beside it, are read
  // again. A task that has just finished is announced to the page, which
  // may want to read itself again.
  const moved=next.tasks.some(task=>{const was=before.get(task.id);return was?was.done!==task.done||was.state!==task.state:task.done>0});
  if(moved&&!first)binChanged();
  for(const task of next.tasks){
    const was=before.get(task.id);
    if(active(task)||!(awaited.delete(task.id)||(was&&active(was))))continue;
    window.dispatchEvent(new CustomEvent<Task>(TASK_FINISHED,{detail:task}));
  }
  for(const listener of listeners)listener();
}

/** Sent with the task when one finishes. */
export const TASK_FINISHED='cull:task-finished';

function schedule(){
  clearTimeout(timer);
  if(listeners.size===0)return;
  timer=setTimeout(()=>void read(),awaited.size>0||(list&&list.active>0)?busyEvery:idleEvery);
}

/** Reads the list now, then carries on at the pace it calls for. A read
 * already on its way is shared, unless `fresh` asks for one sent after now,
 * as a read that must show a task just queued does. */
export function read(fresh=false):Promise<void>{
  if(reading)return fresh?reading.then(()=>read()):reading;
  reading=(async()=>{
    try{
      const response=await fetch('/api/tasks',{cache:'no-store'});
      if(response.ok)publish(await response.json() as TaskList);
    }catch{/* The next read tries again. */}
    finally{reading=null;schedule()}
  })();
  return reading;
}

function subscribe(listener:()=>void){
  listeners.add(listener);
  if(listeners.size===1)void read();
  return()=>{listeners.delete(listener);if(listeners.size===0)clearTimeout(timer)};
}

/** The task list as last read, or null before the first read. */
export function useTasks(){return useSyncExternalStore(subscribe,()=>list)}

async function send(path:string,body:unknown,fallback:string):Promise<Task>{
  const response=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
  if(!response.ok)throw new Error(await failure(response,fallback));
  const task=await response.json() as Task;
  // The new task shows at once, before the next read lists it.
  if(active(task))awaited.add(task.id);
  if(list&&!list.tasks.some(item=>item.id===task.id))publish({tasks:[task,...list.tasks],active:list.active+(active(task)?1:0)});
  void read(true);
  return task;
}

/** Queues screenshots to move into the Bin or copy into the archive. */
export function queueScreenshots(assetIds:number[],action:'remove'|'keep'){
  return send('/api/tasks/screenshots',{assetIds,action},action==='remove'?'The screenshots could not be queued for the Bin.':'The screenshots could not be queued for the archive.');
}

/** Queues restoring or deleting cards of the Bin. */
export function queueBin(action:'restore'|'delete'|'purge-now'|'empty',keys:string[],confirmation?:string){
  return send('/api/tasks/bin',{action,...(action==='empty'?{}:{keys}),...(confirmation?{confirmation}:{})},action==='restore'?'The files could not be queued to restore.':'The files could not be queued to delete.');
}

/** Stops a task, tries its leftovers again, or takes it back. */
export function taskAction(id:string,action:'cancel'|'retry'|'undo'){
  return send(`/api/tasks/${encodeURIComponent(id)}/${action}`,{},action==='cancel'?'The task could not be stopped.':action==='retry'?'The task could not be tried again.':'The task could not be taken back.');
}

/** Takes finished tasks off the list. */
export async function clearTasks(){
  const response=await fetch('/api/tasks/clear',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'});
  if(!response.ok)throw new Error(await failure(response,'The finished tasks could not be cleared.'));
  publish(await response.json() as TaskList);
}

/** Resolves with the task once it has finished. */
export function waitForTask(id:string):Promise<Task>{
  return new Promise(resolve=>{
    const check=()=>{
      const task=list?.tasks.find(item=>item.id===id);
      if(task&&!active(task)){stop();resolve(task)}
    };
    const stop=subscribe(check);
    check();
    void read(true);
  });
}

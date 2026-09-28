// Saves still on their way to the server. The app moves between pages without
// reloading, and a page left while a choice is being saved would otherwise
// show the next page from before it; so moving waits for these first, as a
// fresh load once waited for the journals to be replayed.
const inFlight=new Set<Promise<unknown>>();

/** Marks work as a save to wait for, and hands it back. */
export function tracked<T>(work:Promise<T>):Promise<T>{
  inFlight.add(work);
  const done=()=>{inFlight.delete(work)};
  work.then(done,done);
  return work;
}

/** Waits until nothing is being saved. A save that fails is not an error
 * here: its page says so, and its journal is replayed. */
export async function settled(){
  while(inFlight.size)await Promise.allSettled(inFlight);
}

// Day and night follow the reader's own clock rather than the operating
// system: the app is used in the evening on a laptop that stays light all day.
// A reader can pin either theme in Settings; the choice lives in this browser
// only, like any other display preference.

export type ThemeChoice='auto'|'day'|'night';
export type Theme='day'|'night';

/** Local hours when the day theme starts and ends. */
export const dayStartsAt=7,nightStartsAt=19;

const storageKey='cull-theme';
/** Fired on window whenever the applied theme may have changed. */
export const themeEvent='cull-theme';

export function readChoice():ThemeChoice{
  try{
    const stored=localStorage.getItem(storageKey);
    return stored==='day'||stored==='night'?stored:'auto';
  }catch{return 'auto'}
}

export function themeFor(choice:ThemeChoice,now=new Date()):Theme{
  if(choice!=='auto')return choice;
  const hour=now.getHours();
  return hour>=dayStartsAt&&hour<nightStartsAt?'day':'night';
}

export function applyTheme(){
  const theme=themeFor(readChoice());
  const root=document.documentElement;
  if(root.dataset.theme!==theme){
    root.dataset.theme=theme;
    window.dispatchEvent(new Event(themeEvent));
  }
}

export function saveChoice(choice:ThemeChoice){
  try{
    if(choice==='auto')localStorage.removeItem(storageKey);else localStorage.setItem(storageKey,choice);
  }catch{/* a private window keeps the choice for this page only */}
  applyTheme();
  window.dispatchEvent(new Event(themeEvent));
}

/** Re-checks the clock each minute and whenever the tab comes back, so a page
 * left open through the evening turns to night on its own. */
export function startThemeClock(){
  applyTheme();
  window.setInterval(applyTheme,60_000);
  document.addEventListener('visibilitychange',()=>{if(!document.hidden)applyTheme()});
  window.addEventListener('storage',event=>{if(event.key===storageKey)applyTheme()});
}

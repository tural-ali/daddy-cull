// The search field goes to a date, because a date is how this archive is filed.
// A full date opens that day; a day and month without a year opens that date in
// every year, which is what Today shows. Anything else is refused with an
// example rather than guessed at.

const months=['jan','feb','mar','apr','may','jun','jul','aug','sep','oct','nov','dec'];

function two(value:number){return String(value).padStart(2,'0')}

function valid(year:number|null,month:number,day:number){
  if(month<1||month>12||day<1)return false;
  // 2024 is a leap year, so 29 February is allowed when the year is not given.
  return day<=new Date(year??2024,month,0).getDate();
}

function target(year:number|null,month:number,day:number){
  if(!valid(year,month,day))return null;
  return year===null?`/on/${two(month)}-${two(day)}`:`/day/${year}-${two(month)}-${two(day)}`;
}

function monthOf(word:string){
  const index=months.indexOf(word.slice(0,3).toLowerCase());
  return index<0||!/^[a-z]+$/i.test(word)?0:index+1;
}

/** The page a typed date opens, or null when it is not a date. */
export function pathForDate(input:string):string|null{
  const text=input.trim().replace(/,/g,' ').replace(/\s+/g,' ');
  let match=text.match(/^(\d{4})-(\d{1,2})-(\d{1,2})$/);
  if(match)return target(+match[1],+match[2],+match[3]);
  // Written the British way: day first.
  match=text.match(/^(\d{1,2})[./](\d{1,2})[./](\d{4})$/);
  if(match)return target(+match[3],+match[2],+match[1]);
  match=text.match(/^(\d{1,2})(?:st|nd|rd|th)? ([a-z]+)(?: (\d{4}))?$/i);
  if(match&&monthOf(match[2]))return target(match[3]?+match[3]:null,monthOf(match[2]),+match[1]);
  match=text.match(/^([a-z]+) (\d{1,2})(?:st|nd|rd|th)?(?: (\d{4}))?$/i);
  if(match&&monthOf(match[1]))return target(match[3]?+match[3]:null,monthOf(match[1]),+match[2]);
  return null;
}

/** The day page a file is filed under, found from the first date in its path
 * (the day folder, or a screenshot's date-stamped name), or null. */
export function dayOfPath(path:string):string|null{
  const match=path.match(/(?:^|\/)(\d{4})-(\d{2})-(\d{2})(?=[/_ .]|$)/);
  return match?target(+match[1],+match[2],+match[3]):null;
}

/** "1 March 2021" for a /day/ path. */
export function dayName(dayPath:string){
  const [year,month,day]=dayPath.slice(5).split('-').map(Number);
  return new Date(year,month-1,day).toLocaleDateString('en-GB',{day:'numeric',month:'long',year:'numeric'});
}

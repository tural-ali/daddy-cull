// Material Symbols (Apache 2.0), the icon set Google Photos uses: outlined at
// rest, filled for the page you are on. Each is inlined so it takes currentColor.

import Photo from '@material-symbols/svg-400/outlined/photo.svg?raw';
import PhotoFill from '@material-symbols/svg-400/outlined/photo-fill.svg?raw';
import Calendar from '@material-symbols/svg-400/outlined/calendar_month.svg?raw';
import CalendarFill from '@material-symbols/svg-400/outlined/calendar_month-fill.svg?raw';
import Copies from '@material-symbols/svg-400/outlined/filter_none.svg?raw';
import CopiesFill from '@material-symbols/svg-400/outlined/filter_none-fill.svg?raw';
import Layers from '@material-symbols/svg-400/outlined/layers.svg?raw';
import LayersFill from '@material-symbols/svg-400/outlined/layers-fill.svg?raw';
import Screenshot from '@material-symbols/svg-400/outlined/screenshot_region.svg?raw';
import ScreenshotFill from '@material-symbols/svg-400/outlined/screenshot_region-fill.svg?raw';
import Forum from '@material-symbols/svg-400/outlined/forum.svg?raw';
import ForumFill from '@material-symbols/svg-400/outlined/forum-fill.svg?raw';
import CloudSync from '@material-symbols/svg-400/outlined/cloud_sync.svg?raw';
import CloudSyncFill from '@material-symbols/svg-400/outlined/cloud_sync-fill.svg?raw';
import History from '@material-symbols/svg-400/outlined/history.svg?raw';
import HistoryFill from '@material-symbols/svg-400/outlined/history-fill.svg?raw';
import Delete from '@material-symbols/svg-400/outlined/delete.svg?raw';
import DeleteFill from '@material-symbols/svg-400/outlined/delete-fill.svg?raw';
import Settings from '@material-symbols/svg-400/outlined/settings.svg?raw';
import SettingsFill from '@material-symbols/svg-400/outlined/settings-fill.svg?raw';
import Search from '@material-symbols/svg-400/outlined/search.svg?raw';
import SearchFill from '@material-symbols/svg-400/outlined/search-fill.svg?raw';
import Menu from '@material-symbols/svg-400/outlined/menu.svg?raw';
import MenuFill from '@material-symbols/svg-400/outlined/menu-fill.svg?raw';
import Close from '@material-symbols/svg-400/outlined/close.svg?raw';
import CloseFill from '@material-symbols/svg-400/outlined/close-fill.svg?raw';
import OpenNew from '@material-symbols/svg-400/outlined/open_in_new.svg?raw';
import OpenNewFill from '@material-symbols/svg-400/outlined/open_in_new-fill.svg?raw';
import Fire from '@material-symbols/svg-400/outlined/local_fire_department.svg?raw';
import FireFill from '@material-symbols/svg-400/outlined/local_fire_department-fill.svg?raw';
import Done from '@material-symbols/svg-400/outlined/task_alt.svg?raw';
import DoneFill from '@material-symbols/svg-400/outlined/task_alt-fill.svg?raw';

const icons={
  photo:[Photo,PhotoFill],
  open_in_new:[OpenNew,OpenNewFill],
  calendar_month:[Calendar,CalendarFill],
  filter_none:[Copies,CopiesFill],
  layers:[Layers,LayersFill],
  screenshot_region:[Screenshot,ScreenshotFill],
  forum:[Forum,ForumFill],
  cloud_sync:[CloudSync,CloudSyncFill],
  history:[History,HistoryFill],
  delete:[Delete,DeleteFill],
  settings:[Settings,SettingsFill],
  search:[Search,SearchFill],
  menu:[Menu,MenuFill],
  close:[Close,CloseFill],
  task_alt:[Done,DoneFill],
  local_fire_department:[Fire,FireFill],
} as const;

export type IconName=keyof typeof icons;

export function Icon({name,filled=false}:{name:IconName;filled?:boolean}){
  return <span className="icon" aria-hidden="true" dangerouslySetInnerHTML={{__html:icons[name][filled?1:0]}}/>;
}

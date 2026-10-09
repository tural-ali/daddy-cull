import {createRoot} from 'react-dom/client';
import {App} from './faithful/App';
import {Landscape} from './faithful/Landscape';
import '@fontsource-variable/google-sans-flex';
import './faithful/legacy.css';
import {startThemeClock} from './theme';
import {warmPreviews} from './warm';
import {startTouchSelect} from './faithful/touchSelect';

startThemeClock();
warmPreviews();
startTouchSelect();

createRoot(document.getElementById('root')!).render(<><App/><Landscape/></>);

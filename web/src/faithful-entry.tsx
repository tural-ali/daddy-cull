import {createRoot} from 'react-dom/client';
import {App} from './faithful/App';
import '@fontsource-variable/google-sans-flex';
import './faithful/legacy.css';
import {startThemeClock} from './theme';

startThemeClock();

createRoot(document.getElementById('root')!).render(<App/>);

import {createRoot} from 'react-dom/client';
import {App} from './faithful/App';
import './faithful/legacy.css';
import {startThemeClock} from './theme';

startThemeClock();

createRoot(document.getElementById('root')!).render(<App/>);

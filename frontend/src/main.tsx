import '@mantine/core/styles.css';
import '@mantine/dates/styles.css';
import 'dayjs/locale/ko';

import { createRoot } from 'react-dom/client';
import { App } from './app/App';

const pretendard = new FontFace(
  'Pretendard Variable',
  "url('/PretendardVariable.woff2') format('woff2-variations')",
  { display: 'swap', style: 'normal', weight: '45 920' },
);
document.fonts.add(pretendard);
void pretendard.load();

const root = document.getElementById('root');
if (!root) throw new Error('Cineko root element is missing');
createRoot(root).render(<App />);

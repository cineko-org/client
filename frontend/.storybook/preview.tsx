import '@mantine/core/styles.css';
import '@mantine/dates/styles.css';
import 'pretendard/dist/web/variable/pretendardvariable.css';
import type { Preview } from '@storybook/react-vite';
import { MantineProvider } from '@mantine/core';
import { DatesProvider } from '@mantine/dates';
import { cinekoTheme } from '../src/app/theme';

const preview: Preview = {
  parameters: {
    layout: 'fullscreen',
    backgrounds: { disable: true },
    controls: { expanded: true },
    options: { showPanel: true, storySort: { order: ['Client', ['Application', 'Pages', 'States', 'Overlays', 'Components']] } },
  },
  decorators: [(Story) => (
    <MantineProvider forceColorScheme="dark" theme={cinekoTheme}>
      <DatesProvider settings={{ locale: 'ko', firstDayOfWeek: 0, weekendDays: [0, 6] }}><Story /></DatesProvider>
    </MantineProvider>
  )],
};

export default preview;

import { MantineProvider } from '@mantine/core';
import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AppShellView } from '../src/features/shell/ui/AppShellView';
import { cinekoTheme } from '../src/app/theme';

beforeEach(() => {
	vi.stubGlobal('matchMedia', vi.fn<(query: string) => MediaQueryList>().mockImplementation((query) => ({
		matches: false, media: query, onchange: null,
		addEventListener: () => undefined, removeEventListener: () => undefined,
		addListener: () => undefined, removeListener: () => undefined,
		dispatchEvent: () => true,
	})));
});

afterEach(() => vi.unstubAllGlobals());

describe('application shell', () => {
	it('keeps the desktop sidebar above the phone breakpoint', () => {
		const noop = vi.fn<() => void>();
		const { container } = render(
			<MantineProvider theme={cinekoTheme}>
				<AppShellView
					activeSection="home"
					loading={false}
					connection={{ status: 'ready', message: '', lastSuccessfulAt: '', retrying: false }}
					account={{ status: 'unauthenticated', authenticated: false }}
					network={{ mode: 'direct' }}
					desktopAvailable={false}
					unreadNotices={0}
					feedback={null}
					onNavigate={noop}
					onExit={noop}
					onOpenNotifications={noop}
					onOpenSettings={noop}
					onDismissFeedback={noop}
					onRetryConnection={noop}
				>
					<div>본문</div>
				</AppShellView>
			</MantineProvider>,
		);

		expect(screen.getByRole('navigation')).not.toBeNull();
		expect(container.querySelector('footer')?.classList.contains('mantine-hidden-from-xs')).toBe(true);
	});
});

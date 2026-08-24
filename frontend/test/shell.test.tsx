import { MantineProvider } from '@mantine/core';
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AppShellView } from '../src/features/shell/ui/AppShellView';
import { cinekoTheme } from '../src/app/theme';
import { directNetwork, unauthenticatedAccount } from '../src/stories/fixtures';

beforeEach(() => {
	vi.stubGlobal('matchMedia', vi.fn<(query: string) => MediaQueryList>().mockImplementation((query) => ({
		matches: false, media: query, onchange: null,
		addEventListener: () => undefined, removeEventListener: () => undefined,
		addListener: () => undefined, removeListener: () => undefined,
		dispatchEvent: () => true,
	})));
});

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
});

describe('application shell', () => {
	it('keeps one navigation model across desktop and mobile surfaces', () => {
		const noop = vi.fn<() => void>();
		const { container } = render(
			<MantineProvider theme={cinekoTheme}>
				<AppShellView
					activeSection="home"
					loading={false}
					connection={{ status: 'ready', message: '', lastSuccessfulAt: '', retrying: false }}
					account={unauthenticatedAccount}
					network={directNetwork}
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
		expect(container.querySelector('footer')?.classList.contains('mantine-hidden-from-sm')).toBe(true);
		expect(screen.getAllByRole('button', { name: '홈' })).toHaveLength(2);
		expect(screen.getAllByRole('button', { name: '예매 찾기' })).toHaveLength(2);
		expect(screen.getAllByRole('button', { name: '좌석 프리셋' })).toHaveLength(2);
		expect(screen.getAllByRole('button', { name: '관제' })).toHaveLength(2);
	});
});

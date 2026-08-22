import { describe, expect, it } from 'vitest';
import { errorMessage } from '../src/api/client';

describe('client error messages', () => {
	it('surfaces authentication-required execution failures distinctly', () => {
		expect(errorMessage(new Error('CGV authentication is required'))).toBe('CGV 로그인이 필요합니다. 로그인 후 모니터를 다시 실행하세요.');
	});
});

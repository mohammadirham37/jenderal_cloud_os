import { afterEach, describe, expect, it, vi } from 'vitest';
import {
	backoffDelay,
	debounce,
	describeReason,
	isFinalReason,
	shouldReconnect
} from './backoff.ts';

describe('backoffDelay', () => {
	it('doubles from 1s and caps at 30s', () => {
		expect([1, 2, 3, 4, 5, 6, 7, 8].map((a) => backoffDelay(a))).toEqual([
			1000, 2000, 4000, 8000, 16000, 30000, 30000, 30000
		]);
	});

	it('stops after the maximum number of attempts', () => {
		expect(backoffDelay(9)).toBeNull();
		expect(backoffDelay(3, { maxAttempts: 2 })).toBeNull();
	});

	it('rejects invalid attempts', () => {
		expect(backoffDelay(0)).toBeNull();
		expect(backoffDelay(-1)).toBeNull();
		expect(backoffDelay(Number.NaN)).toBeNull();
	});

	it('honours custom options', () => {
		expect(backoffDelay(3, { baseMs: 500, maxMs: 1500 })).toBe(1500);
		expect(backoffDelay(2, { baseMs: 500 })).toBe(1000);
	});
});

describe('close reasons', () => {
	it('does not reconnect after final reasons', () => {
		for (const r of [
			'session_closed',
			'expired',
			'workspace_stopped',
			'login_session_ended',
			'shell_exited'
		]) {
			expect(isFinalReason(r)).toBe(true);
			expect(shouldReconnect(r)).toBe(false);
		}
	});

	it('reconnects after transient failures', () => {
		expect(shouldReconnect('')).toBe(true);
		expect(shouldReconnect(undefined)).toBe(true);
		expect(shouldReconnect('Host agent unavailable')).toBe(true);
		expect(shouldReconnect('client_disconnected')).toBe(true);
	});

	it('never reconnects when the user closed the tab', () => {
		expect(shouldReconnect('', true)).toBe(false);
	});

	it('describes reasons in plain English', () => {
		expect(describeReason('expired')).toMatch(/expired/);
		expect(describeReason('')).toBe('The connection was lost.');
		expect(describeReason('Host agent unavailable')).toBe('Host agent unavailable');
	});
});

describe('debounce', () => {
	afterEach(() => {
		vi.useRealTimers();
	});

	it('only calls once with the last arguments', () => {
		vi.useFakeTimers();
		const fn = vi.fn();
		const d = debounce(fn, 100);
		d.call(1);
		d.call(2);
		vi.advanceTimersByTime(99);
		expect(fn).not.toHaveBeenCalled();
		vi.advanceTimersByTime(1);
		expect(fn).toHaveBeenCalledExactlyOnceWith(2);
	});

	it('can be cancelled', () => {
		vi.useFakeTimers();
		const fn = vi.fn();
		const d = debounce(fn, 100);
		d.call();
		d.cancel();
		vi.advanceTimersByTime(200);
		expect(fn).not.toHaveBeenCalled();
	});
});

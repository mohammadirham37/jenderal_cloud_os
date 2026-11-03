import { describe, expect, it } from 'vitest';
import { bytes, cores, percent, relative, duration, titleCase } from './format';

describe('format', () => {
	it('formats bytes with binary units', () => {
		expect(bytes(0)).toBe('0 B');
		expect(bytes(1536)).toBe('1.5 KiB');
		expect(bytes(5 * 1024 ** 3)).toBe('5.0 GiB');
		expect(bytes(null)).toBe('—');
		expect(bytes(437.6027406)).toBe('438 B');
	});
	it('formats cpu millicores', () => {
		expect(cores(1000)).toBe('1 core');
		expect(cores(2500)).toBe('2.5 cores');
		expect(cores(500)).toBe('0.5 cores');
	});
	it('formats percent and missing values honestly', () => {
		expect(percent(42.4)).toBe('42%');
		expect(percent(undefined)).toBe('—');
	});
	it('formats relative times and durations', () => {
		const now = Date.parse('2026-01-01T12:00:00Z');
		expect(relative('2026-01-01T11:58:00Z', now)).toContain('2');
		expect(duration(3 * 3600 + 120)).toBe('3h 2m');
		expect(titleCase('workspace_files')).toBe('Workspace Files');
	});
});

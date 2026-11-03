import { describe, expect, it } from 'vitest';
import type { FileEntry } from '#lib/api/types.ts';
import {
	basename,
	join,
	nameError,
	normalize,
	parent,
	segments,
	sortEntries,
	within
} from './paths.ts';

const entry = (name: string, type: FileEntry['type'] = 'file'): FileEntry => ({
	name,
	path: name,
	type,
	size: 0,
	mode: '',
	mod_time: ''
});

describe('paths', () => {
	it('normalizes', () => {
		expect(normalize('/a//b/./c/')).toBe('a/b/c');
		expect(normalize(null)).toBe('');
	});

	it('joins, splits and nests', () => {
		expect(join('', 'x')).toBe('x');
		expect(join('a/b', 'c')).toBe('a/b/c');
		expect(parent('a/b/c')).toBe('a/b');
		expect(parent('a')).toBe('');
		expect(basename('a/b/c.txt')).toBe('c.txt');
		expect(within('a/b', 'a')).toBe(true);
		expect(within('ab', 'a')).toBe(false);
		expect(within('x', '')).toBe(true);
	});

	it('builds breadcrumbs', () => {
		expect(segments('src/app')).toEqual([
			{ name: 'Home', path: '' },
			{ name: 'src', path: 'src' },
			{ name: 'app', path: 'src/app' }
		]);
	});

	it('validates names', () => {
		expect(nameError('')).toBeTruthy();
		expect(nameError('a/b')).toBeTruthy();
		expect(nameError('..')).toBeTruthy();
		expect(nameError('main.py')).toBeNull();
	});

	it('sorts directories first', () => {
		const sorted = sortEntries([entry('b.txt'), entry('z', 'dir'), entry('a10'), entry('a2')]);
		expect(sorted.map((e) => e.name)).toEqual(['z', 'a2', 'a10', 'b.txt']);
	});
});

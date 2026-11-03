import { describe, expect, it } from 'vitest';
import { appForPath, parseWindowName, visibleApps } from './apps';

describe('desktop app registry', () => {
	it('maps routes to the owning app, file manager first', () => {
		expect(appForPath('/workspaces/11111111-2222-3333-4444-555555555555/files')?.id).toBe('files');
		expect(appForPath('/workspaces/11111111-2222-3333-4444-555555555555')?.id).toBe('workspaces');
		expect(appForPath('/admin/users/abc')?.id).toBe('users');
		expect(appForPath('/terminal')?.id).toBe('terminal');
		expect(appForPath('/nowhere')).toBeUndefined();
	});
	it('filters apps by permission', () => {
		const ids = visibleApps((...p) => p.includes('terminals.open')).map((a) => a.id);
		expect(ids).toContain('terminal');
		expect(ids).toContain('dashboard');
		expect(ids).not.toContain('users');
	});
	it('parses window names', () => {
		expect(parseWindowName('itk-win:files:k3x9')).toEqual({ appId: 'files', windowId: 'k3x9' });
		expect(parseWindowName('evil')).toBeNull();
	});
});

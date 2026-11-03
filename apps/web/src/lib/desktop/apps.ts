// Application registry of the Jenderal Cloud OS desktop. Each app is an existing
// console route rendered inside a window (same-origin iframe in embedded
// mode). Visibility follows the same permissions as the classic navigation.
import {
	Activity,
	Archive,
	Bell,
	Boxes,
	Cpu,
	Database,
	FolderKanban,
	FolderOpen,
	Gauge,
	HardDrive,
	LayoutDashboard,
	LayoutTemplate,
	Layers,
	Megaphone,
	ScrollText,
	Server,
	Settings,
	Shield,
	SquareTerminal,
	UserRound,
	Users,
	Wrench
} from '@lucide/svelte';
import type { Component } from 'svelte';

export type AppCategory = 'Workspace' | 'Data' | 'System' | 'Administration';

export interface DesktopApp {
	id: string;
	title: string;
	/** One-line description shown in the start menu. */
	blurb: string;
	icon: Component;
	url: string;
	category: AppCategory;
	/** Visible when the user holds any of these permissions (empty = everyone). */
	perms?: string[];
	/** Shown as an icon on the desktop. */
	desktop?: boolean;
	/** Only one window of this app (re-opening focuses it). */
	singleton?: boolean;
	/** Tile tint (CSS colour). */
	tint: string;
	size?: { w: number; h: number };
	/** Route patterns owned by this app (first match wins, most specific first). */
	match: RegExp[];
}

const uuid = '[0-9a-f-]{36}';

export const apps: DesktopApp[] = [
	{
		id: 'terminal',
		title: 'Terminal',
		blurb: 'Shell in your running workspaces',
		icon: SquareTerminal,
		url: '/terminal',
		category: 'Workspace',
		perms: ['terminals.open'],
		desktop: true,
		singleton: true,
		tint: '#1f2937',
		size: { w: 960, h: 600 },
		match: [/^\/terminal/]
	},
	{
		id: 'files',
		title: 'Files',
		blurb: 'Browse and edit workspace files',
		icon: FolderOpen,
		url: '/files',
		category: 'Workspace',
		perms: ['files.manage'],
		desktop: true,
		tint: '#d97706',
		size: { w: 1000, h: 640 },
		match: [new RegExp(`^/workspaces/${uuid}/files`), /^\/files/]
	},
	{
		id: 'workspaces',
		title: 'Workspaces',
		blurb: 'Create, start and manage environments',
		icon: Layers,
		url: '/workspaces',
		category: 'Workspace',
		perms: ['workspaces.view_own', 'workspaces.view_all'],
		desktop: true,
		singleton: true,
		tint: '#0f766e',
		size: { w: 1080, h: 680 },
		match: [/^\/workspaces/]
	},
	{
		id: 'dashboard',
		title: 'Overview',
		blurb: 'Your quota, activity and announcements',
		icon: LayoutDashboard,
		url: '/dashboard',
		category: 'Workspace',
		desktop: true,
		singleton: true,
		tint: '#2563eb',
		size: { w: 1060, h: 680 },
		match: [/^\/dashboard/]
	},
	{
		id: 'projects',
		title: 'Projects',
		blurb: 'Teams, members and shared resources',
		icon: FolderKanban,
		url: '/projects',
		category: 'Workspace',
		desktop: true,
		singleton: true,
		tint: '#7c3aed',
		match: [/^\/projects/]
	},
	{
		id: 'containers',
		title: 'Containers',
		blurb: 'Running containers, logs and stats',
		icon: Boxes,
		url: '/containers',
		category: 'Workspace',
		perms: ['workspaces.view_own', 'containers.manage_all'],
		singleton: true,
		tint: '#0891b2',
		match: [/^\/containers/]
	},
	{
		id: 'templates',
		title: 'Templates',
		blurb: 'Ready-made development environments',
		icon: LayoutTemplate,
		url: '/templates',
		category: 'Workspace',
		perms: ['templates.view'],
		singleton: true,
		tint: '#65a30d',
		match: [/^\/templates/]
	},
	{
		id: 'databases',
		title: 'Databases',
		blurb: 'PostgreSQL databases for projects',
		icon: Database,
		url: '/databases',
		category: 'Data',
		desktop: true,
		singleton: true,
		tint: '#4f46e5',
		match: [/^\/databases/]
	},
	{
		id: 'gpu',
		title: 'GPU Center',
		blurb: 'Request and monitor GPU accelerators',
		icon: Cpu,
		url: '/gpu',
		category: 'Data',
		perms: ['gpu.view', 'gpu.request', 'gpu.approve'],
		desktop: true,
		singleton: true,
		tint: '#16a34a',
		match: [/^\/gpu/]
	},
	{
		id: 'monitoring',
		title: 'Monitoring',
		blurb: 'Host, workload and GPU metrics',
		icon: Activity,
		url: '/monitoring',
		category: 'System',
		perms: ['monitoring.view_host'],
		desktop: true,
		singleton: true,
		tint: '#dc2626',
		size: { w: 1120, h: 720 },
		match: [/^\/monitoring/]
	},
	{
		id: 'notifications',
		title: 'Notifications',
		blurb: 'Alerts, approvals and announcements',
		icon: Bell,
		url: '/notifications',
		category: 'System',
		singleton: true,
		tint: '#ea580c',
		size: { w: 760, h: 620 },
		match: [/^\/notifications/]
	},
	{
		id: 'account',
		title: 'My Account',
		blurb: 'Profile, password, MFA and sessions',
		icon: UserRound,
		url: '/account',
		category: 'System',
		singleton: true,
		tint: '#475569',
		size: { w: 860, h: 640 },
		match: [/^\/account/]
	},
	{
		id: 'users',
		title: 'Users',
		blurb: 'Accounts, roles and suspensions',
		icon: Users,
		url: '/admin/users',
		category: 'Administration',
		perms: ['users.view'],
		desktop: true,
		singleton: true,
		tint: '#be185d',
		match: [/^\/admin\/users/]
	},
	{
		id: 'roles',
		title: 'Roles',
		blurb: 'Permissions and role ranks',
		icon: Shield,
		url: '/admin/roles',
		category: 'Administration',
		perms: ['roles.view', 'roles.manage'],
		singleton: true,
		tint: '#9333ea',
		match: [/^\/admin\/roles/]
	},
	{
		id: 'quotas',
		title: 'Quotas',
		blurb: 'Resource packages',
		icon: Gauge,
		url: '/admin/quotas',
		category: 'Administration',
		perms: ['quotas.manage'],
		singleton: true,
		tint: '#0d9488',
		match: [/^\/admin\/quotas/]
	},
	{
		id: 'admin-templates',
		title: 'Template Studio',
		blurb: 'Manage workspace templates',
		icon: Wrench,
		url: '/admin/templates',
		category: 'Administration',
		perms: ['templates.manage'],
		singleton: true,
		tint: '#ca8a04',
		match: [/^\/admin\/templates/]
	},
	{
		id: 'images',
		title: 'Images',
		blurb: 'Container image allowlist',
		icon: HardDrive,
		url: '/admin/images',
		category: 'Administration',
		perms: ['images.manage'],
		singleton: true,
		tint: '#0369a1',
		match: [/^\/admin\/images/]
	},
	{
		id: 'host',
		title: 'Host & Agent',
		blurb: 'Agent status and emergency stop',
		icon: Server,
		url: '/admin/host',
		category: 'Administration',
		perms: ['host.operations.view', 'agent.view'],
		desktop: true,
		singleton: true,
		tint: '#334155',
		match: [/^\/admin\/host/]
	},
	{
		id: 'announcements',
		title: 'Announcements',
		blurb: 'Broadcast to all users',
		icon: Megaphone,
		url: '/admin/announcements',
		category: 'Administration',
		perms: ['announcements.manage'],
		singleton: true,
		tint: '#c2410c',
		match: [/^\/admin\/announcements/]
	},
	{
		id: 'audit',
		title: 'Audit Log',
		blurb: 'Who did what, and when',
		icon: ScrollText,
		url: '/admin/audit',
		category: 'Administration',
		perms: ['audit.view'],
		singleton: true,
		tint: '#57534e',
		size: { w: 1120, h: 700 },
		match: [/^\/admin\/audit/]
	},
	{
		id: 'backups',
		title: 'Backups',
		blurb: 'Encrypted backups and restore',
		icon: Archive,
		url: '/admin/backups',
		category: 'Administration',
		perms: ['backup.manage'],
		singleton: true,
		tint: '#15803d',
		match: [/^\/admin\/backups/]
	},
	{
		id: 'settings',
		title: 'Platform Settings',
		blurb: 'Security, quotas, alerts, backups',
		icon: Settings,
		url: '/admin/settings',
		category: 'Administration',
		perms: ['settings.manage'],
		singleton: true,
		tint: '#52525b',
		match: [/^\/admin\/settings/]
	}
];

export const categories: AppCategory[] = ['Workspace', 'Data', 'System', 'Administration'];

export function appById(id: string): DesktopApp | undefined {
	return apps.find((a) => a.id === id);
}

/** Returns the app that owns a path (most specific match). */
export function appForPath(path: string): DesktopApp | undefined {
	for (const a of apps) {
		if (a.id === 'files' && a.match.some((r) => r.test(path))) return a;
	}
	return apps.find((a) => a.match.some((r) => r.test(path)));
}

/** Apps the user may open. */
export function visibleApps(can: (...perms: string[]) => boolean): DesktopApp[] {
	return apps.filter((a) => !a.perms || a.perms.length === 0 || can(...a.perms));
}

/** Parses the iframe window name "itk-win:<appId>:<windowId>". */
export function parseWindowName(name: string): { appId: string; windowId: string } | null {
	const m = /^itk-win:([a-z-]+):([a-z0-9]+)$/.exec(name);
	return m ? { appId: m[1], windowId: m[2] } : null;
}

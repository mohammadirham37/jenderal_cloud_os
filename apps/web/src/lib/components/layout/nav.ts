import {
	Activity,
	Bell,
	Boxes,
	CloudDownload,
	Cpu,
	Database,
	FolderKanban,
	Gauge,
	HardDrive,
	KeyRound,
	LayoutDashboard,
	LayoutTemplate,
	Megaphone,
	ScrollText,
	Server,
	Settings,
	Shield,
	SquareTerminal,
	Users,
	Archive,
	Layers
} from '@lucide/svelte';
import type { Component } from 'svelte';

export interface NavItem {
	href: string;
	label: string;
	icon: Component;
	/** Visible when the user holds any of these permissions (empty = everyone). */
	perms?: string[];
}

export interface NavGroup {
	label: string;
	items: NavItem[];
}

export const navGroups: NavGroup[] = [
	{
		label: 'Workspace',
		items: [
			{ href: '/dashboard', label: 'Dashboard', icon: LayoutDashboard },
			{
				href: '/workspaces',
				label: 'Workspaces',
				icon: Layers,
				perms: ['workspaces.view_own', 'workspaces.view_all']
			},
			{ href: '/terminal', label: 'Terminal', icon: SquareTerminal, perms: ['terminals.open'] },
			{ href: '/projects', label: 'Projects', icon: FolderKanban },
			{
				href: '/containers',
				label: 'Containers',
				icon: Boxes,
				perms: ['workspaces.view_own', 'containers.manage_all']
			},
			{ href: '/databases', label: 'Databases', icon: Database },
			{
				href: '/gpu',
				label: 'GPU Center',
				icon: Cpu,
				perms: ['gpu.view', 'gpu.request', 'gpu.approve']
			},
			{ href: '/templates', label: 'Templates', icon: LayoutTemplate, perms: ['templates.view'] }
		]
	},
	{
		label: 'Operations',
		items: [
			{ href: '/monitoring', label: 'Monitoring', icon: Activity, perms: ['monitoring.view_host'] },
			{ href: '/notifications', label: 'Notifications', icon: Bell }
		]
	},
	{
		label: 'Administration',
		items: [
			{ href: '/admin/users', label: 'Users', icon: Users, perms: ['users.view'] },
			{
				href: '/admin/roles',
				label: 'Roles & Permissions',
				icon: Shield,
				perms: ['roles.view', 'roles.manage']
			},
			{
				href: '/admin/quotas',
				label: 'Quotas',
				icon: Gauge,
				perms: ['quotas.manage']
			},
			{
				href: '/admin/templates',
				label: 'Template Management',
				icon: LayoutTemplate,
				perms: ['templates.manage']
			},
			{
				href: '/admin/images',
				label: 'Image Allowlist',
				icon: HardDrive,
				perms: ['images.manage']
			},
			{
				href: '/admin/host',
				label: 'Host & Agent',
				icon: Server,
				perms: ['host.operations.view', 'agent.view']
			},
			{
				href: '/admin/updates',
				label: 'System Update',
				icon: CloudDownload,
				perms: ['system.update', 'host.operations.view']
			},
			{
				href: '/admin/announcements',
				label: 'Announcements',
				icon: Megaphone,
				perms: ['announcements.manage']
			},
			{ href: '/admin/audit', label: 'Audit Logs', icon: ScrollText, perms: ['audit.view'] },
			{ href: '/admin/backups', label: 'Backups', icon: Archive, perms: ['backup.manage'] },
			{
				href: '/admin/settings',
				label: 'Platform Settings',
				icon: Settings,
				perms: ['settings.manage']
			}
		]
	},
	{
		label: 'Account',
		items: [{ href: '/account', label: 'My Account', icon: KeyRound }]
	}
];

/** Human labels for breadcrumb segments. */
export const segmentLabels: Record<string, string> = {
	workspaces: 'Workspaces',
	new: 'New',
	files: 'Files',
	terminal: 'Terminal',
	ide: 'VS Code',
	browser: 'Web Browser',
	projects: 'Projects',
	containers: 'Containers',
	databases: 'Databases',
	gpu: 'GPU Center',
	templates: 'Templates',
	monitoring: 'Monitoring',
	notifications: 'Notifications',
	admin: 'Administration',
	users: 'Users',
	roles: 'Roles & Permissions',
	quotas: 'Quotas',
	images: 'Image Allowlist',
	host: 'Host & Agent',
	announcements: 'Announcements',
	audit: 'Audit Logs',
	backups: 'Backups',
	settings: 'Settings',
	account: 'My Account',
	dashboard: 'Dashboard',
	password: 'Password'
};

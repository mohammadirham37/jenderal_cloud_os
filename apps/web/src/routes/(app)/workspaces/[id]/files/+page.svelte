<script lang="ts">
	import { onDestroy, onMount, untrack } from 'svelte';
	import { beforeNavigate, goto } from '$app/navigation';
	import { page } from '$app/state';
	import {
		ArrowLeft,
		ChevronRight,
		Download,
		FilePlus,
		FolderLock,
		FolderPlus,
		FolderX,
		Pencil,
		RefreshCw,
		ShieldAlert,
		Trash2,
		Upload,
		X
	} from '@lucide/svelte';
	import {
		Alert,
		Button,
		Card,
		DataTable,
		EmptyState,
		ErrorState,
		Field,
		Modal,
		PageHeader,
		Skeleton,
		StatusBadge
	} from '#lib/components/ui/index.ts';
	import { api, ApiError, errorMessage } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { FileEntry, WorkspaceDetail } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
	import { appConfig } from '#lib/stores/config.svelte.ts';
	import { crumbs } from '#lib/stores/breadcrumb.svelte.ts';
	import { bytes, dateTime, relative } from '#lib/format.ts';
	import FileIcon from '#lib/components/files/FileIcon.svelte';
	import FolderTree from '#lib/components/files/FolderTree.svelte';
	import FileEditor from '#lib/components/files/FileEditor.svelte';
	import { FileClient, type DirListing } from '#lib/components/files/fileClient.svelte.ts';
	import { isEmergencyRequired } from '#lib/components/files/emergency.ts';
	import {
		basename,
		join,
		nameError,
		normalize,
		parent,
		segments,
		sortEntries,
		typeLabel
	} from '#lib/components/files/paths.ts';

	const id = $derived(page.params.id ?? '');
	const fc = $derived(new FileClient(id));
	const ws = resource(() => api.get<WorkspaceDetail>(`/workspaces/${id}`));
	const path = $derived(normalize(page.url.searchParams.get('path')));

	const FILE_STATUSES = ['ready', 'running', 'stopped'];
	const canManage = $derived(
		!!ws.data &&
			ws.data.grants.Edit &&
			auth.can('files.manage') &&
			FILE_STATUSES.includes(ws.data.status)
	);
	const uploadMax = $derived(appConfig.value?.upload_max_bytes ?? 0);

	onMount(() => {
		appConfig.load();
	});

	$effect(() => {
		if (ws.data) crumbs.set(ws.data.id, ws.data.name);
	});

	// ---- Directory listing -------------------------------------------------
	let listing = $state<DirListing | null>(null);
	let listLoading = $state(false);
	let listError = $state<unknown>(null);
	let treeVersion = $state(0);
	let selected = $state<string | null>(null);
	let seq = 0;

	async function loadList(p = path) {
		const my = ++seq;
		listLoading = true;
		try {
			const l = await fc.list(p);
			if (my === seq) {
				listing = l;
				listError = null;
			}
		} catch (e) {
			if (my === seq) {
				listError = e;
				listing = null;
			}
		} finally {
			if (my === seq) listLoading = false;
		}
	}

	$effect(() => {
		const p = path;
		if (canManage) untrack(() => loadList(p));
	});

	const entries = $derived(listing ? sortEntries(listing.entries) : []);

	function refresh() {
		treeVersion++;
		loadList();
	}

	function navigate(p: string) {
		selected = null;
		const target = p ? `${page.url.pathname}?path=${encodeURIComponent(p)}` : page.url.pathname;
		goto(target, { reset: false });
	}

	function open(e: FileEntry) {
		if (e.type === 'dir') navigate(e.path);
		else openFile(e.path);
	}

	const treeLoad = (p: string) => fc.list(p).then((l) => l.entries);

	// ---- Editor ------------------------------------------------------------
	interface EditorState {
		path: string;
		content: string;
		original: string;
		modTime: string;
		size: number;
		saving: boolean;
		conflict: string | null;
	}
	let editor = $state<EditorState | null>(null);
	const dirty = $derived(!!editor && editor.content !== editor.original);
	let opening = $state<string | null>(null);
	let unsupported = $state<{ open: boolean; path: string; message: string }>({
		open: false,
		path: '',
		message: ''
	});

	async function openFile(p: string) {
		opening = p;
		try {
			const f = await fc.read(p);
			editor = {
				path: f.path || p,
				content: f.content,
				original: f.content,
				modTime: f.mod_time,
				size: f.size,
				saving: false,
				conflict: null
			};
		} catch (e) {
			if (e instanceof ApiError && (e.status === 415 || e.status === 413)) {
				unsupported = { open: true, path: p, message: e.message };
			} else {
				toast.error('Could not open file', errorMessage(e));
			}
		} finally {
			opening = null;
		}
	}

	async function save(force = false) {
		const ed = editor;
		if (!ed || ed.saving) return;
		ed.saving = true;
		const content = ed.content;
		try {
			const res = await fc.write(ed.path, content, {
				expectedModTime: force ? undefined : ed.modTime
			});
			ed.original = content;
			ed.modTime = res.mod_time;
			ed.size = res.size;
			ed.conflict = null;
			toast.success('File saved', ed.path);
		} catch (e) {
			if (e instanceof ApiError && e.status === 409) {
				ed.conflict = e.message;
			} else if (e instanceof ApiError && e.status === 413) {
				toast.error('File too large', e.message);
			} else {
				toast.error('Could not save file', errorMessage(e));
			}
		} finally {
			ed.saving = false;
		}
	}

	async function closeEditor() {
		if (dirty) {
			const ok = await confirmDialog.confirm({
				title: 'Discard unsaved changes?',
				message: 'Your changes to this file have not been saved and will be lost.',
				confirmLabel: 'Discard changes',
				danger: true
			});
			if (!ok) return;
		}
		editor = null;
		loadList();
	}

	function reloadEditor() {
		if (editor) openFile(editor.path);
	}

	let bypassGuard = false;
	beforeNavigate((nav) => {
		if (bypassGuard || !dirty) return;
		nav.cancel();
		if (nav.type === 'leave') return; // the browser shows its own prompt
		const to = nav.to?.url;
		confirmDialog
			.confirm({
				title: 'Leave without saving?',
				message: `You have unsaved changes in ${editor?.path}. They will be lost if you leave this page.`,
				confirmLabel: 'Leave page',
				danger: true
			})
			.then(async (ok) => {
				if (!ok || !to) return;
				bypassGuard = true;
				try {
					editor = null;
					await goto(to);
				} finally {
					bypassGuard = false;
				}
			});
	});

	// ---- Download ----------------------------------------------------------
	async function downloadWithReason(p: string) {
		try {
			await fc.downloadWithReason(p, basename(p));
		} catch (err) {
			toast.error('Download failed', errorMessage(err));
		}
	}

	// ---- New folder / new file / rename ------------------------------------
	let folderModal = $state({ open: false, name: '', error: '', busy: false });
	let fileModal = $state({ open: false, name: '', error: '', busy: false });
	let renameModal = $state({ open: false, from: '', to: '', isDir: false, error: '', busy: false });

	function fieldError(e: unknown, field: string): string {
		if (e instanceof ApiError) return e.details[field] ?? e.message;
		return errorMessage(e);
	}

	async function createFolder(ev: SubmitEvent) {
		ev.preventDefault();
		const err = nameError(folderModal.name);
		if (err) {
			folderModal.error = err;
			return;
		}
		folderModal.busy = true;
		try {
			const p = join(path, folderModal.name.trim());
			await fc.mkdir(p);
			toast.success('Folder created', p);
			folderModal.open = false;
			refresh();
		} catch (e) {
			folderModal.error = fieldError(e, 'path');
		} finally {
			folderModal.busy = false;
		}
	}

	async function createFile(ev: SubmitEvent) {
		ev.preventDefault();
		const err = nameError(fileModal.name);
		if (err) {
			fileModal.error = err;
			return;
		}
		fileModal.busy = true;
		try {
			const p = join(path, fileModal.name.trim());
			await fc.write(p, '', { create: true });
			toast.success('File created', p);
			fileModal.open = false;
			refresh();
			await openFile(p);
		} catch (e) {
			fileModal.error = fieldError(e, 'path');
		} finally {
			fileModal.busy = false;
		}
	}

	function startRename(e: FileEntry) {
		renameModal = {
			open: true,
			from: e.path,
			to: e.path,
			isDir: e.type === 'dir',
			error: '',
			busy: false
		};
	}

	async function rename(ev: SubmitEvent) {
		ev.preventDefault();
		const to = normalize(renameModal.to);
		if (!to) {
			renameModal.error = 'Enter the new path.';
			return;
		}
		if (to === renameModal.from) {
			renameModal.open = false;
			return;
		}
		renameModal.busy = true;
		try {
			await fc.rename(renameModal.from, to);
			toast.success(renameModal.isDir ? 'Folder renamed' : 'File renamed', to);
			renameModal.open = false;
			refresh();
		} catch (e) {
			renameModal.error = fieldError(e, 'to');
		} finally {
			renameModal.busy = false;
		}
	}

	async function remove(e: FileEntry) {
		const isDir = e.type === 'dir';
		const r = await confirmDialog.ask({
			title: isDir ? `Delete folder "${e.name}"?` : `Delete "${e.name}"?`,
			message: isDir
				? 'The folder and everything inside it will be permanently deleted. This cannot be undone.'
				: 'The file will be permanently deleted. This cannot be undone.',
			confirmLabel: 'Delete',
			danger: true,
			requireText: isDir ? e.name : undefined
		});
		if (!r.confirmed) return;
		try {
			await fc.remove(e.path, isDir);
			toast.success(isDir ? 'Folder deleted' : 'File deleted', e.path);
			if (selected === e.path) selected = null;
			refresh();
		} catch (err) {
			toast.error('Could not delete', errorMessage(err));
		}
	}

	// ---- Uploads -------------------------------------------------------------
	interface UploadItem {
		id: number;
		name: string;
		size: number;
		dir: string;
		progress: number;
		status: 'queued' | 'uploading' | 'done' | 'error' | 'skipped';
		message?: string;
	}
	let uploads = $state<UploadItem[]>([]);
	let uploading = $state(false);
	let fileInput = $state<HTMLInputElement>();
	let dragDepth = $state(0);
	let nextUploadId = 1;
	let queue: { item: UploadItem; file: File }[] = [];
	let abort = new AbortController();

	function enqueue(files: FileList | File[] | null | undefined) {
		const list = Array.from(files ?? []);
		if (list.length === 0) return;
		for (const f of list) {
			uploads.push({
				id: nextUploadId++,
				name: f.name,
				size: f.size,
				dir: path,
				progress: 0,
				status: 'queued'
			});
			const item = uploads[uploads.length - 1];
			if (uploadMax > 0 && f.size > uploadMax) {
				item.status = 'error';
				item.message = `Larger than the upload limit of ${bytes(uploadMax)}. Use the terminal (e.g. git or curl) for large files.`;
				continue;
			}
			queue.push({ item, file: f });
		}
		if (!uploading) pump();
	}

	async function pump() {
		uploading = true;
		let done = 0;
		while (queue.length > 0) {
			const job = queue.shift()!;
			const it = job.item;
			it.status = 'uploading';
			const send = (overwrite: boolean) =>
				fc.upload(job.file, it.dir, overwrite, (f) => (it.progress = f), abort.signal);
			try {
				await send(false);
				it.status = 'done';
				done++;
			} catch (e) {
				if (e instanceof ApiError && e.status === 409) {
					const ok = await confirmDialog.confirm({
						title: `Replace "${it.name}"?`,
						message: `A file named "${it.name}" already exists in ${it.dir || 'Home'}. Replacing it overwrites its contents.`,
						confirmLabel: 'Replace file',
						danger: true
					});
					if (ok) {
						try {
							it.progress = 0;
							await send(true);
							it.status = 'done';
							done++;
						} catch (e2) {
							it.status = 'error';
							it.message = errorMessage(e2);
						}
					} else {
						it.status = 'skipped';
						it.message = 'Skipped: a file with this name already exists.';
					}
				} else {
					it.status = 'error';
					it.message = errorMessage(e);
				}
			}
		}
		uploading = false;
		if (done > 0) {
			toast.success(`Uploaded ${done} ${done === 1 ? 'file' : 'files'}`);
			refresh();
		}
	}

	function cancelUploads() {
		for (const q of queue) {
			q.item.status = 'skipped';
			q.item.message = 'Cancelled.';
		}
		queue = [];
		abort.abort();
		abort = new AbortController();
	}

	function clearFinished() {
		uploads = uploads.filter((u) => u.status === 'queued' || u.status === 'uploading');
	}

	function hasFiles(e: DragEvent) {
		return !!e.dataTransfer && Array.from(e.dataTransfer.types).includes('Files');
	}

	onDestroy(() => {
		queue = [];
		abort.abort();
	});

	const columns = [
		{ key: 'name', label: 'Name' },
		{ key: 'size', label: 'Size', class: 'w-28 text-right' },
		{ key: 'modified', label: 'Modified', class: 'w-40' },
		{ key: 'type', label: 'Type', class: 'w-32 hidden md:table-cell' },
		{ key: 'actions', label: 'Actions', class: 'w-40 text-right' }
	];

	function statusExplanation(status: string): string {
		switch (status) {
			case 'requested':
			case 'provisioning':
				return 'The workspace is still being provisioned. Files become available once it is ready.';
			case 'suspended':
				return 'The workspace is suspended. Files are unavailable until an administrator resumes it.';
			case 'failed':
				return 'Provisioning of this workspace failed, so its storage is not available.';
			case 'deleting':
			case 'deleted':
				return 'The workspace is being deleted.';
			default:
				return `Files are not available while the workspace is ${status}.`;
		}
	}
</script>

<PageHeader
	title={ws.data ? `Files · ${ws.data.name}` : 'Files'}
	description="Browse, upload and edit files in the workspace home directory."
>
	{#snippet meta()}
		{#if ws.data}
			<StatusBadge status={ws.data.status} pending={ws.data.pending_job} />
			<span>{ws.data.template_name}</span>
		{/if}
	{/snippet}
	{#snippet actions()}
		<Button href="/workspaces/{id}" variant="ghost"
			><ArrowLeft class="size-4" /> Back to workspace</Button
		>
	{/snippet}
</PageHeader>

{#if ws.error}
	<Card><ErrorState error={ws.error} retry={ws.reload} /></Card>
{:else if !ws.data}
	<div class="grid gap-4 lg:grid-cols-[15rem_minmax(0,1fr)]">
		<div class="card p-4"><Skeleton lines={6} /></div>
		<div class="card p-4"><Skeleton lines={8} /></div>
	</div>
{:else if !ws.data.grants.Edit || !auth.can('files.manage')}
	<Card>
		<EmptyState
			icon={FolderLock}
			title="No file access"
			description="Managing files requires owner or editor access to this workspace. Ask the workspace owner to add you as an editor."
		>
			{#snippet action()}<Button href="/workspaces/{id}">Back to workspace</Button>{/snippet}
		</EmptyState>
	</Card>
{:else if !FILE_STATUSES.includes(ws.data.status)}
	<Card>
		<EmptyState
			icon={FolderX}
			title="Files are not available right now"
			description={statusExplanation(ws.data.status)}
		>
			{#snippet action()}
				<Button onclick={ws.reload} loading={ws.loading}>
					<RefreshCw class="size-4" /> Check again
				</Button>
			{/snippet}
		</EmptyState>
	</Card>
{:else}
	{#if ws.data.grants.Emergency}
		<div class="mb-4">
			<Alert tone="warning" title="Emergency access">
				This workspace belongs to {ws.data.owner_username}. You are accessing its files under
				emergency access; every action is recorded in the audit log with your reason.
			</Alert>
		</div>
	{/if}

	{#if editor}
		<FileEditor
			path={editor.path}
			bind:value={editor.content}
			{dirty}
			saving={editor.saving}
			size={editor.size}
			modTime={editor.modTime}
			conflict={editor.conflict}
			downloadHref={fc.reason ? undefined : fc.downloadUrl(editor.path)}
			ondownload={() => editor && downloadWithReason(editor.path)}
			onsave={() => save(false)}
			onoverwrite={() => save(true)}
			onreload={reloadEditor}
			onclose={closeEditor}
		/>
	{:else}
		<div class="grid gap-4 lg:grid-cols-[15rem_minmax(0,1fr)]">
			<aside class="card max-h-[75vh] overflow-auto p-2">
				<FolderTree selected={path} version={treeVersion} load={treeLoad} onselect={navigate} />
			</aside>

			<section class="card min-w-0" aria-label="Folder contents">
				<div
					class="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3"
				>
					<nav aria-label="Current folder" class="min-w-0">
						<ol class="flex flex-wrap items-center gap-1 text-sm">
							{#each segments(path) as s, i (s.path)}
								{#if i > 0}<li aria-hidden="true">
										<ChevronRight class="size-3.5 text-fg-subtle" />
									</li>{/if}
								<li>
									{#if s.path === path}
										<span class="font-medium text-fg" aria-current="page">{s.name}</span>
									{:else}
										<button
											type="button"
											class="text-fg-muted hover:text-fg hover:underline"
											onclick={() => navigate(s.path)}>{s.name}</button
										>
									{/if}
								</li>
							{/each}
						</ol>
					</nav>
					<div class="flex flex-wrap items-center gap-2">
						<input
							bind:this={fileInput}
							type="file"
							multiple
							class="sr-only"
							tabindex="-1"
							aria-hidden="true"
							onchange={(e) => {
								enqueue(e.currentTarget.files);
								e.currentTarget.value = '';
							}}
						/>
						<Button size="sm" variant="primary" onclick={() => fileInput?.click()}>
							<Upload class="size-3.5" /> Upload
						</Button>
						<Button
							size="sm"
							onclick={() => (folderModal = { open: true, name: '', error: '', busy: false })}
						>
							<FolderPlus class="size-3.5" /> New folder
						</Button>
						<Button
							size="sm"
							onclick={() => (fileModal = { open: true, name: '', error: '', busy: false })}
						>
							<FilePlus class="size-3.5" /> New file
						</Button>
						<Button
							size="sm"
							variant="ghost"
							onclick={refresh}
							loading={listLoading}
							aria-label="Refresh"
							title="Refresh"
						>
							{#if !listLoading}<RefreshCw class="size-3.5" />{/if}
						</Button>
					</div>
				</div>

				{#if uploads.length > 0}
					<div class="border-b border-border px-4 py-3" aria-live="polite">
						<div class="mb-2 flex items-center justify-between gap-2">
							<h2 class="text-xs font-semibold tracking-wide text-fg-muted uppercase">Uploads</h2>
							<div class="flex gap-2">
								{#if uploading}
									<Button size="sm" variant="ghost" onclick={cancelUploads}>Cancel</Button>
								{:else}
									<Button size="sm" variant="ghost" onclick={clearFinished}>Clear</Button>
								{/if}
							</div>
						</div>
						<ul class="space-y-2">
							{#each uploads as u (u.id)}
								<li class="text-sm">
									<div class="flex items-center justify-between gap-3">
										<span class="truncate font-medium">{u.name}</span>
										<span
											class="shrink-0 text-xs {u.status === 'error'
												? 'text-danger'
												: u.status === 'done'
													? 'text-success'
													: 'text-fg-muted'}"
										>
											{#if u.status === 'uploading'}{Math.round(u.progress * 100)}% of {bytes(
													u.size
												)}
											{:else if u.status === 'done'}Uploaded
											{:else if u.status === 'error'}Failed
											{:else if u.status === 'skipped'}Skipped
											{:else}Waiting{/if}
										</span>
									</div>
									{#if u.status === 'uploading' || u.status === 'done'}
										<div
											class="mt-1 h-1.5 overflow-hidden rounded-full bg-panel-2"
											role="progressbar"
											aria-label="Upload progress for {u.name}"
											aria-valuemin={0}
											aria-valuemax={100}
											aria-valuenow={Math.round(u.progress * 100)}
										>
											<div
												class="h-full rounded-full {u.status === 'done'
													? 'bg-success'
													: 'bg-accent'} transition-[width]"
												style="width: {Math.round(u.progress * 100)}%"
											></div>
										</div>
									{/if}
									{#if u.message}<p
											class="mt-0.5 text-xs {u.status === 'error'
												? 'text-danger'
												: 'text-fg-muted'}"
										>
											{u.message}
										</p>{/if}
								</li>
							{/each}
						</ul>
					</div>
				{/if}

				<div
					class="relative min-h-[50vh]"
					role="region"
					aria-label="File list. Drop files here to upload them to the current folder."
					ondragenter={(e) => {
						if (!hasFiles(e)) return;
						e.preventDefault();
						dragDepth++;
					}}
					ondragover={(e) => {
						if (!hasFiles(e)) return;
						e.preventDefault();
						if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy';
					}}
					ondragleave={(e) => {
						if (!hasFiles(e)) return;
						dragDepth = Math.max(0, dragDepth - 1);
					}}
					ondrop={(e) => {
						if (!hasFiles(e)) return;
						e.preventDefault();
						dragDepth = 0;
						enqueue(e.dataTransfer?.files);
					}}
				>
					{#if dragDepth > 0}
						<div
							class="pointer-events-none absolute inset-2 z-10 flex flex-col items-center justify-center rounded-lg border-2 border-dashed border-accent bg-accent-soft/90 text-accent"
						>
							<Upload class="mb-2 size-6" aria-hidden="true" />
							<p class="text-sm font-medium">Drop files to upload to {path || 'Home'}</p>
							{#if uploadMax > 0}<p class="text-xs">Up to {bytes(uploadMax)} per file</p>{/if}
						</div>
					{/if}

					{#if listing?.truncated}
						<div class="px-4 pt-3">
							<Alert tone="warning" title="Folder listing truncated">
								This folder contains more entries than can be shown. Only the first
								{listing.entries.length} are listed; use the terminal to work with very large folders.
							</Alert>
						</div>
					{/if}

					{#if listError && isEmergencyRequired(listError)}
						<EmptyState
							icon={ShieldAlert}
							title="A reason is required"
							description="This workspace belongs to another user. Provide a justification to access its files under emergency access."
						>
							{#snippet action()}<Button onclick={() => loadList()}>Provide a reason</Button
								>{/snippet}
						</EmptyState>
					{:else if listError instanceof ApiError && listError.status === 404}
						<EmptyState
							icon={FolderX}
							title="Folder not found"
							description="This folder does not exist (it may have been moved or deleted)."
						>
							{#snippet action()}
								<Button onclick={() => navigate(parent(path))}>Go to parent folder</Button>
							{/snippet}
						</EmptyState>
					{:else}
						<DataTable
							{columns}
							rows={entries}
							loading={listLoading}
							error={listError}
							retry={() => loadList()}
						>
							{#snippet empty()}
								<EmptyState
									icon={Upload}
									title="This folder is empty"
									description="Upload files (or drag and drop them here), or create a new file or folder."
								>
									{#snippet action()}
										<Button variant="primary" onclick={() => fileInput?.click()}>
											<Upload class="size-4" /> Upload files
										</Button>
									{/snippet}
								</EmptyState>
							{/snippet}
							{#snippet row(e: FileEntry)}
								<tr class={selected === e.path ? 'bg-accent-soft/60' : 'hover:bg-panel-2/60'}>
									<td class="px-4 py-2">
										<button
											type="button"
											class="flex max-w-full items-center gap-2 text-left font-medium text-fg"
											title={e.type === 'dir'
												? 'Double-click or press Enter to open'
												: 'Double-click or press Enter to edit'}
											aria-pressed={selected === e.path}
											onclick={() => (selected = e.path)}
											ondblclick={() => open(e)}
											onkeydown={(ev) => {
												if (ev.key === 'Enter') {
													ev.preventDefault();
													open(e);
												}
											}}
										>
											<FileIcon entry={e} />
											<span class="truncate">{e.name}</span>
											{#if opening === e.path}<span class="text-xs text-fg-muted">Opening…</span
												>{/if}
										</button>
									</td>
									<td class="px-4 py-2 text-right whitespace-nowrap text-fg-muted tabular-nums">
										{e.type === 'dir' ? '—' : bytes(e.size)}
									</td>
									<td
										class="px-4 py-2 whitespace-nowrap text-fg-muted"
										title={dateTime(e.mod_time)}
									>
										{relative(e.mod_time)}
									</td>
									<td class="hidden px-4 py-2 text-fg-muted md:table-cell">{typeLabel(e)}</td>
									<td class="px-4 py-2">
										<div class="flex justify-end gap-1">
											{#if e.type === 'dir'}
												<Button size="sm" variant="ghost" onclick={() => open(e)}>Open</Button>
											{:else}
												<Button
													size="sm"
													variant="ghost"
													onclick={() => open(e)}
													aria-label="Edit {e.name}"
													title="Edit"><Pencil class="size-3.5" /></Button
												>
												{#if fc.reason}
													<Button
														size="sm"
														variant="ghost"
														onclick={() => downloadWithReason(e.path)}
														aria-label="Download {e.name}"
														title="Download"><Download class="size-3.5" /></Button
													>
												{:else}
													<a
														href={fc.downloadUrl(e.path)}
														download={e.name}
														class="inline-flex h-8 items-center rounded-md px-2.5 text-fg-muted hover:bg-panel-2 hover:text-fg"
														aria-label="Download {e.name}"
														title="Download"><Download class="size-3.5" aria-hidden="true" /></a
													>
												{/if}
											{/if}
											<Button
												size="sm"
												variant="ghost"
												onclick={() => startRename(e)}
												aria-label="Rename or move {e.name}"
												title="Rename / move"
											>
												<span class="text-xs">Rename</span>
											</Button>
											<Button
												size="sm"
												variant="ghost"
												onclick={() => remove(e)}
												aria-label="Delete {e.name}"
												title="Delete"><Trash2 class="size-3.5 text-danger" /></Button
											>
										</div>
									</td>
								</tr>
							{/snippet}
						</DataTable>
					{/if}
				</div>
				<p class="border-t border-border px-4 py-2 text-xs text-fg-muted">
					{entries.length}
					{entries.length === 1 ? 'item' : 'items'}{#if uploadMax > 0}
						· uploads up to {bytes(uploadMax)} per file{/if} · drag and drop files onto the list to upload
				</p>
			</section>
		</div>
	{/if}
{/if}

<Modal
	bind:open={folderModal.open}
	title="New folder"
	description="Create a folder in {path || 'Home'}."
	size="sm"
>
	<form id="new-folder-form" onsubmit={createFolder} novalidate>
		<Field label="Folder name" id="folder-name" error={folderModal.error} required>
			<input
				id="folder-name"
				class="input"
				bind:value={folderModal.name}
				autocomplete="off"
				aria-invalid={!!folderModal.error}
				aria-describedby={folderModal.error ? 'folder-name-error' : undefined}
			/>
		</Field>
	</form>
	{#snippet footer()}
		<Button onclick={() => (folderModal.open = false)}>Cancel</Button>
		<Button type="submit" form="new-folder-form" variant="primary" loading={folderModal.busy}
			>Create folder</Button
		>
	{/snippet}
</Modal>

<Modal
	bind:open={fileModal.open}
	title="New file"
	description="Create an empty text file in {path || 'Home'} and open it in the editor."
	size="sm"
>
	<form id="new-file-form" onsubmit={createFile} novalidate>
		<Field
			label="File name"
			id="file-name"
			error={fileModal.error}
			hint="Include an extension, e.g. main.py or notes.md."
			required
		>
			<input
				id="file-name"
				class="input"
				bind:value={fileModal.name}
				autocomplete="off"
				aria-invalid={!!fileModal.error}
				aria-describedby={fileModal.error ? 'file-name-error' : 'file-name-hint'}
			/>
		</Field>
	</form>
	{#snippet footer()}
		<Button onclick={() => (fileModal.open = false)}>Cancel</Button>
		<Button type="submit" form="new-file-form" variant="primary" loading={fileModal.busy}
			>Create file</Button
		>
	{/snippet}
</Modal>

<Modal
	bind:open={renameModal.open}
	title={renameModal.isDir ? 'Rename or move folder' : 'Rename or move file'}
	description="Current path: {renameModal.from}"
	size="md"
>
	<form id="rename-form" onsubmit={rename} novalidate>
		<Field
			label="New path"
			id="rename-to"
			error={renameModal.error}
			hint="Path relative to the workspace home. Change the folder part to move it, e.g. archive/{basename(
				renameModal.from
			)}."
			required
		>
			<input
				id="rename-to"
				class="input font-mono"
				bind:value={renameModal.to}
				autocomplete="off"
				aria-invalid={!!renameModal.error}
				aria-describedby={renameModal.error ? 'rename-to-error' : 'rename-to-hint'}
			/>
		</Field>
	</form>
	{#snippet footer()}
		<Button onclick={() => (renameModal.open = false)}>Cancel</Button>
		<Button type="submit" form="rename-form" variant="primary" loading={renameModal.busy}
			>Save</Button
		>
	{/snippet}
</Modal>

<Modal bind:open={unsupported.open} title="Cannot open in the editor" size="sm">
	<p class="text-sm text-fg-muted">{unsupported.message}</p>
	<p class="mt-2 font-mono text-xs break-all text-fg-subtle">{unsupported.path}</p>
	{#snippet footer()}
		<Button onclick={() => (unsupported.open = false)}><X class="size-4" /> Close</Button>
		{#if fc.reason}
			<Button
				variant="primary"
				onclick={() => {
					const p = unsupported.path;
					unsupported.open = false;
					downloadWithReason(p);
				}}><Download class="size-4" /> Download</Button
			>
		{:else}
			<a
				href={fc.downloadUrl(unsupported.path)}
				download={basename(unsupported.path)}
				class="inline-flex h-9 items-center gap-2 rounded-md bg-accent px-3.5 text-sm font-medium text-accent-fg hover:bg-accent-hover"
				onclick={() => (unsupported.open = false)}
				><Download class="size-4" aria-hidden="true" /> Download</a
			>
		{/if}
	{/snippet}
</Modal>

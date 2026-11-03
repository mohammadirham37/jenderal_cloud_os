<script lang="ts" module>
	export type ConnState =
		| { kind: 'connecting' }
		| { kind: 'connected' }
		| { kind: 'reconnecting'; attempt: number; delayMs: number; reason: string }
		| { kind: 'disconnected'; reason: string; final: boolean };
</script>

<script lang="ts">
	// One browser terminal bound to a terminal session. Bytes are relayed over
	// a WebSocket authenticated by the session cookie (no secrets in the URL):
	// binary frames carry terminal data, text frames carry JSON control
	// messages ({"type":"resize",cols,rows} / {"type":"ping"}).
	import { onMount } from 'svelte';
	import { Terminal, type ITheme } from '@xterm/xterm';
	import { FitAddon } from '@xterm/addon-fit';
	import { WebLinksAddon } from '@xterm/addon-web-links';
	import '@xterm/xterm/css/xterm.css';
	import {
		ClipboardPaste,
		Copy,
		Eraser,
		Maximize2,
		Minimize2,
		Minus,
		Plus,
		RotateCcw,
		SquareTerminal
	} from '@lucide/svelte';
	import { Button } from '#lib/components/ui/index.ts';
	import { api } from '#lib/api/client.ts';
	import type { TerminalSession, Workspace } from '#lib/api/types.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { backoffDelay, debounce, describeReason, isFinalReason } from './backoff.ts';

	let {
		session,
		active = true,
		onended,
		onstatus,
		onrestart,
		resumed = false
	}: {
		session: TerminalSession;
		/** Whether the tab is visible (refits and focuses when it becomes visible). */
		active?: boolean;
		/** Called once when the session is over (final close reason). */
		onended?: (reason: string) => void;
		onstatus?: (state: ConnState) => void;
		/** Opens a new session for the same workspace/container. */
		onrestart?: () => void | Promise<void>;
		/** True when attaching to a session that was opened earlier (shows a notice). */
		resumed?: boolean;
	} = $props();

	const FONT_KEY = 'itk-terminal-font-size';
	const PING_MS = 25000;

	let wrapper = $state<HTMLDivElement>();
	let host = $state<HTMLDivElement>();
	let conn = $state<ConnState>({ kind: 'connecting' });
	let fontSize = $state(14);
	let fullscreen = $state(false);
	let notice = $state('');
	let restarting = $state(false);

	let term: Terminal | null = null;
	let fit: FitAddon | null = null;
	let socket: WebSocket | null = null;
	let attempt = 0;
	let everConnected = false;
	let disposed = false;
	let ended = false;
	let retryTimer: ReturnType<typeof setTimeout> | null = null;
	let pingTimer: ReturnType<typeof setInterval> | null = null;
	let noticeTimer: ReturnType<typeof setTimeout> | null = null;
	const encoder = new TextEncoder();

	function setConn(s: ConnState) {
		conn = s;
		onstatus?.(s);
	}

	// Readable palettes (GitHub light/dark) for both UI themes.
	const lightTheme: ITheme = {
		background: '#ffffff',
		foreground: '#1f2328',
		cursor: '#0f766e',
		cursorAccent: '#ffffff',
		selectionBackground: '#b6e3dc',
		black: '#24292f',
		red: '#cf222e',
		green: '#116329',
		yellow: '#7d4e00',
		blue: '#0550ae',
		magenta: '#8250df',
		cyan: '#1b7c83',
		white: '#6e7781',
		brightBlack: '#57606a',
		brightRed: '#a40e26',
		brightGreen: '#1a7f37',
		brightYellow: '#633c01',
		brightBlue: '#218bff',
		brightMagenta: '#a475f9',
		brightCyan: '#3192aa',
		brightWhite: '#8c959f'
	};
	const darkTheme: ITheme = {
		background: '#0d1117',
		foreground: '#e6edf3',
		cursor: '#2dd4bf',
		cursorAccent: '#0d1117',
		selectionBackground: '#1f4f4a',
		black: '#484f58',
		red: '#ff7b72',
		green: '#3fb950',
		yellow: '#d29922',
		blue: '#58a6ff',
		magenta: '#bc8cff',
		cyan: '#39c5cf',
		white: '#b1bac4',
		brightBlack: '#6e7681',
		brightRed: '#ffa198',
		brightGreen: '#56d364',
		brightYellow: '#e3b341',
		brightBlue: '#79c0ff',
		brightMagenta: '#d2a8ff',
		brightCyan: '#56d4dd',
		brightWhite: '#ffffff'
	};
	const isDark = () => document.documentElement.classList.contains('dark');
	let dark = $state(false);

	function send(data: string | Uint8Array<ArrayBuffer>) {
		if (socket && socket.readyState === WebSocket.OPEN) socket.send(data);
	}

	function sendResize(cols: number, rows: number) {
		send(JSON.stringify({ type: 'resize', cols, rows }));
	}
	const resizeLater = debounce(sendResize, 150);

	function safeFit() {
		if (!term || !fit || !host || host.clientWidth === 0 || host.clientHeight === 0) return;
		try {
			fit.fit();
		} catch {
			// renderer not ready yet
		}
	}

	function writeNotice(text: string) {
		term?.write(`\r\n\x1b[2m[${text}]\x1b[0m\r\n`);
	}

	function showNotice(text: string) {
		notice = text;
		if (noticeTimer) clearTimeout(noticeTimer);
		noticeTimer = setTimeout(() => (notice = ''), 8000);
	}

	function clearTimers() {
		if (retryTimer) clearTimeout(retryTimer);
		if (pingTimer) clearInterval(pingTimer);
		retryTimer = null;
		pingTimer = null;
	}

	function wsUrl(): string {
		return (
			(location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + session.websocket_path
		);
	}

	function connect() {
		if (disposed || ended) return;
		clearTimers();
		if (attempt === 0) setConn({ kind: 'connecting' });
		let opened = false;
		const ws = new WebSocket(wsUrl());
		ws.binaryType = 'arraybuffer';
		socket = ws;

		ws.onopen = () => {
			if (ws !== socket || disposed) return;
			opened = true;
			const wasReconnect = everConnected;
			everConnected = true;
			attempt = 0;
			setConn({ kind: 'connected' });
			safeFit();
			if (term) sendResize(term.cols, term.rows);
			pingTimer = setInterval(() => send('{"type":"ping"}'), PING_MS);
			if (!wasReconnect && resumed) {
				showNotice(
					'Attached to an existing session. A new shell was started — earlier shell state is not preserved.'
				);
			}
			if (wasReconnect) {
				writeNotice(
					'Reconnected: a new shell was started; the previous shell state was not preserved'
				);
				showNotice('Reconnected. A new shell was started — previous shell state is not preserved.');
			}
			if (active) term?.focus();
		};
		ws.onmessage = (ev) => {
			if (ws !== socket || !term) return;
			if (typeof ev.data === 'string') term.write(ev.data);
			else term.write(new Uint8Array(ev.data as ArrayBuffer));
		};
		ws.onerror = () => {
			// a close event follows
		};
		ws.onclose = (ev) => {
			if (ws !== socket) return;
			socket = null;
			clearTimers();
			if (disposed || ended) return;
			handleClose(ev.reason, opened);
		};
	}

	/** After a failed handshake the browser hides the HTTP status; ask the API why. */
	async function probe(): Promise<string | null> {
		try {
			const list = await api.get<TerminalSession[]>('/terminals');
			const s = list.find((x) => x.id === session.id);
			if (!s) return 'terminal_session_ended';
			if (s.status === 'closed' || s.status === 'expired' || new Date(s.expires_at) <= new Date()) {
				return s.end_reason === 'expired' || s.status === 'expired'
					? 'expired'
					: 'terminal_session_ended';
			}
			const w = await api.get<Workspace>(`/workspaces/${session.workspace_id}`);
			if (w.status !== 'running') return 'workspace_stopped';
		} catch {
			// network trouble or a 404 on the workspace: keep retrying
		}
		return null;
	}

	async function handleClose(reason: string, opened: boolean) {
		if (isFinalReason(reason)) {
			finish(reason);
			return;
		}
		if (opened) writeNotice(`Connection lost: ${describeReason(reason)}`);
		if (!opened) {
			const why = await probe();
			if (disposed || ended) return;
			if (why) {
				finish(why);
				return;
			}
		}
		scheduleRetry(reason);
	}

	function scheduleRetry(reason: string) {
		attempt++;
		const delay = backoffDelay(attempt);
		if (delay === null) {
			setConn({
				kind: 'disconnected',
				reason: `Could not reconnect after several attempts. ${describeReason(reason)}`,
				final: false
			});
			return;
		}
		setConn({ kind: 'reconnecting', attempt, delayMs: delay, reason });
		retryTimer = setTimeout(connect, delay);
	}

	function finish(reason: string) {
		if (ended) return;
		ended = true;
		clearTimers();
		writeNotice(describeReason(reason));
		setConn({ kind: 'disconnected', reason, final: true });
		onended?.(reason);
	}

	function reconnectNow() {
		attempt = 0;
		if (socket) {
			const s = socket;
			socket = null;
			s.close();
		}
		connect();
	}

	async function copySelection() {
		const text = term?.getSelection() ?? '';
		if (!text) {
			toast.info('Nothing selected', 'Select text in the terminal first.');
			return;
		}
		try {
			await navigator.clipboard.writeText(text);
			toast.success('Copied to clipboard');
		} catch {
			toast.error('Copy failed', 'The browser blocked clipboard access.');
		}
		term?.focus();
	}

	async function pasteClipboard() {
		try {
			const text = await navigator.clipboard.readText();
			if (text) term?.paste(text);
		} catch {
			toast.error(
				'Paste failed',
				'The browser blocked clipboard access. Use Ctrl+Shift+V or the context menu instead.'
			);
		}
		term?.focus();
	}

	function setFont(n: number) {
		fontSize = Math.min(28, Math.max(9, n));
		localStorage.setItem(FONT_KEY, String(fontSize));
		if (term) {
			term.options.fontSize = fontSize;
			safeFit();
		}
		term?.focus();
	}

	async function toggleFullscreen() {
		try {
			if (document.fullscreenElement) await document.exitFullscreen();
			else await wrapper?.requestFullscreen();
		} catch {
			toast.error('Fullscreen unavailable', 'Your browser did not allow fullscreen mode.');
		}
	}

	async function restart() {
		if (!onrestart) return;
		restarting = true;
		try {
			await onrestart();
		} finally {
			restarting = false;
		}
	}

	onMount(() => {
		const saved = Number(localStorage.getItem(FONT_KEY));
		if (saved >= 9 && saved <= 28) fontSize = saved;
		dark = isDark();
		const t = new Terminal({
			fontFamily: "'JetBrains Mono', ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
			fontSize,
			cursorBlink: true,
			scrollback: 5000,
			theme: dark ? darkTheme : lightTheme,
			cols: 120,
			rows: 32
		});
		term = t;
		fit = new FitAddon();
		t.loadAddon(fit);
		t.loadAddon(new WebLinksAddon());
		t.open(host!);
		safeFit();

		t.onData((d) => send(encoder.encode(d)));
		t.onBinary((d) => {
			const b = new Uint8Array(d.length);
			for (let i = 0; i < d.length; i++) b[i] = d.charCodeAt(i) & 0xff;
			send(b);
		});
		t.onResize(({ cols, rows }) => resizeLater.call(cols, rows));
		t.attachCustomKeyEventHandler((e) => {
			if (e.type !== 'keydown') return true;
			const mod = e.ctrlKey || e.metaKey;
			if (mod && e.shiftKey && e.key.toLowerCase() === 'c') {
				e.preventDefault();
				copySelection();
				return false;
			}
			if (mod && e.shiftKey && e.key.toLowerCase() === 'v') {
				// Let the browser deliver a native paste event to xterm.
				return false;
			}
			if (e.metaKey && e.key.toLowerCase() === 'c' && t.hasSelection()) {
				return false; // macOS Cmd+C copies natively
			}
			return true;
		});

		const ro = new ResizeObserver(() => safeFit());
		ro.observe(host!);
		const mo = new MutationObserver(() => {
			const d = isDark();
			if (d !== dark) {
				dark = d;
				t.options.theme = d ? darkTheme : lightTheme;
			}
		});
		mo.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
		const onFs = () => {
			fullscreen = document.fullscreenElement === wrapper;
			requestAnimationFrame(safeFit);
		};
		document.addEventListener('fullscreenchange', onFs);
		const onOnline = () => {
			if (conn.kind === 'reconnecting') reconnectNow();
		};
		window.addEventListener('online', onOnline);

		connect();

		return () => {
			disposed = true;
			clearTimers();
			resizeLater.cancel();
			if (noticeTimer) clearTimeout(noticeTimer);
			ro.disconnect();
			mo.disconnect();
			document.removeEventListener('fullscreenchange', onFs);
			window.removeEventListener('online', onOnline);
			if (document.fullscreenElement === wrapper) document.exitFullscreen().catch(() => {});
			const s = socket;
			socket = null;
			s?.close(1000, 'client_closed');
			t.dispose();
			term = null;
			fit = null;
		};
	});

	$effect(() => {
		if (!active) return;
		const raf = requestAnimationFrame(() => {
			safeFit();
			term?.focus();
		});
		return () => cancelAnimationFrame(raf);
	});

	const indicator = $derived.by(() => {
		switch (conn.kind) {
			case 'connecting':
				return { tone: 'text-info', label: 'Connecting…' };
			case 'connected':
				return { tone: 'text-success', label: 'Connected' };
			case 'reconnecting':
				return {
					tone: 'text-warning',
					label: `Reconnecting (attempt ${conn.attempt}) in ${Math.round(conn.delayMs / 1000)}s`
				};
			case 'disconnected':
				return { tone: 'text-danger', label: 'Disconnected' };
		}
	});
</script>

<div
	bind:this={wrapper}
	class="flex flex-col overflow-hidden rounded-lg border border-border bg-panel {fullscreen
		? 'h-screen rounded-none'
		: ''}"
>
	<div
		class="flex flex-wrap items-center justify-between gap-2 border-b border-border bg-panel-2/60 px-3 py-1.5"
	>
		<div class="flex min-w-0 items-center gap-2 text-xs" role="status" aria-live="polite">
			<span class="inline-flex items-center gap-1.5 font-medium {indicator.tone}">
				<span class="size-2 rounded-full bg-current" aria-hidden="true"></span>
				{indicator.label}
			</span>
			{#if conn.kind === 'disconnected'}
				<span class="truncate text-fg-muted">· {describeReason(conn.reason)}</span>
			{/if}
		</div>
		<div class="flex flex-wrap items-center gap-0.5" role="toolbar" aria-label="Terminal tools">
			{#if conn.kind === 'reconnecting' || (conn.kind === 'disconnected' && !conn.final)}
				<Button size="sm" variant="ghost" onclick={reconnectNow}>
					<RotateCcw class="size-3.5" /> Reconnect now
				</Button>
			{/if}
			<Button
				size="sm"
				variant="ghost"
				onclick={copySelection}
				title="Copy selection (Ctrl/Cmd+Shift+C)"
				aria-label="Copy selection"><Copy class="size-3.5" /></Button
			>
			<Button
				size="sm"
				variant="ghost"
				onclick={pasteClipboard}
				title="Paste (Ctrl/Cmd+Shift+V)"
				aria-label="Paste from clipboard"><ClipboardPaste class="size-3.5" /></Button
			>
			<span class="mx-1 h-4 w-px bg-border" aria-hidden="true"></span>
			<Button
				size="sm"
				variant="ghost"
				onclick={() => setFont(fontSize - 1)}
				disabled={fontSize <= 9}
				title="Smaller font"
				aria-label="Decrease font size"><Minus class="size-3.5" /></Button
			>
			<span class="w-8 text-center text-xs text-fg-muted tabular-nums" aria-label="Font size"
				>{fontSize}px</span
			>
			<Button
				size="sm"
				variant="ghost"
				onclick={() => setFont(fontSize + 1)}
				disabled={fontSize >= 28}
				title="Larger font"
				aria-label="Increase font size"><Plus class="size-3.5" /></Button
			>
			<span class="mx-1 h-4 w-px bg-border" aria-hidden="true"></span>
			<Button
				size="sm"
				variant="ghost"
				onclick={() => {
					term?.clear();
					term?.focus();
				}}
				title="Clear scrollback"
				aria-label="Clear terminal"><Eraser class="size-3.5" /></Button
			>
			<Button
				size="sm"
				variant="ghost"
				onclick={toggleFullscreen}
				title={fullscreen ? 'Exit fullscreen' : 'Fullscreen'}
				aria-label={fullscreen ? 'Exit fullscreen' : 'Enter fullscreen'}
				aria-pressed={fullscreen}
			>
				{#if fullscreen}<Minimize2 class="size-3.5" />{:else}<Maximize2 class="size-3.5" />{/if}
			</Button>
		</div>
	</div>

	{#if notice}
		<div class="border-b border-border bg-info-soft px-3 py-1.5 text-xs text-fg" role="status">
			{notice}
		</div>
	{/if}

	<div class="relative min-h-0 flex-1">
		<div
			bind:this={host}
			class="terminal-host {fullscreen
				? 'h-[calc(100vh-2.75rem)]'
				: 'h-[calc(100vh-22rem)] min-h-[60vh]'} w-full p-2"
			style="background: {dark ? darkTheme.background : lightTheme.background}"
			aria-label="Terminal for {session.workspace_name}"
		></div>

		{#if conn.kind === 'disconnected' && conn.final}
			<div
				class="absolute inset-x-0 bottom-0 flex flex-wrap items-center justify-between gap-3 border-t border-border bg-panel/95 px-4 py-3"
			>
				<p class="text-sm text-fg">
					{describeReason(conn.reason)}
					{#if conn.reason === 'shell_exited'}You can open a fresh shell.{/if}
				</p>
				{#if onrestart && conn.reason !== 'login_session_ended'}
					<Button size="sm" variant="primary" onclick={restart} loading={restarting}>
						<SquareTerminal class="size-3.5" />
						{conn.reason === 'shell_exited' ? 'Reopen terminal' : 'Start new session'}
					</Button>
				{/if}
			</div>
		{/if}
	</div>
</div>

<style>
	.terminal-host :global(.xterm) {
		height: 100%;
	}
</style>

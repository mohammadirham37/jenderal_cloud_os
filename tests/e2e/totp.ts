// RFC 6238 TOTP (SHA-1, 6 digits, 30 s) for completing MFA in browser tests.
import { createHmac } from 'node:crypto';

function base32Decode(input: string): Buffer {
	const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
	const clean = input.replace(/[\s=]/g, '').toUpperCase();
	let bits = 0;
	let value = 0;
	const out: number[] = [];
	for (const ch of clean) {
		const idx = alphabet.indexOf(ch);
		if (idx < 0) throw new Error(`invalid base32 character ${ch}`);
		value = (value << 5) | idx;
		bits += 5;
		if (bits >= 8) {
			out.push((value >>> (bits - 8)) & 0xff);
			bits -= 8;
		}
	}
	return Buffer.from(out);
}

export function totp(secret: string, step = Math.floor(Date.now() / 1000 / 30)): string {
	const msg = Buffer.alloc(8);
	msg.writeBigUInt64BE(BigInt(step));
	const h = createHmac('sha1', base32Decode(secret)).update(msg).digest();
	const off = h[h.length - 1] & 0x0f;
	const bin = ((h[off] & 0x7f) << 24) | (h[off + 1] << 16) | (h[off + 2] << 8) | h[off + 3];
	return String(bin % 1_000_000).padStart(6, '0');
}

/** Returns a code for a step that has not been used yet (server rejects replays). */
export class TotpSource {
	constructor(
		private secret: string,
		private last = 0
	) {}
	/** Last step used (persist it to continue in another test file). */
	get lastStep(): number {
		return this.last;
	}
	async next(): Promise<string> {
		for (;;) {
			const cur = Math.floor(Date.now() / 1000 / 30);
			const step = Math.max(cur - 1, this.last + 1);
			if (step <= cur + 1) {
				this.last = step;
				return totp(this.secret, step);
			}
			await new Promise((r) => setTimeout(r, 1000));
		}
	}
}

// Small statistics shared by the collector and the report.

/** Mean of the finite numbers in xs, or null when there are none. */
export function mean(xs) {
	const v = xs.filter((x) => Number.isFinite(x));
	return v.length ? v.reduce((a, b) => a + b, 0) / v.length : null;
}

/** Spread of the finite numbers in xs: min, max and population standard deviation. */
export function spread(xs) {
	const v = xs.filter((x) => Number.isFinite(x));
	if (!v.length) return { n: 0, min: null, max: null, sd: null };
	const m = mean(v);
	const sd = Math.sqrt(v.reduce((a, b) => a + (b - m) ** 2, 0) / v.length);
	return { n: v.length, min: Math.min(...v), max: Math.max(...v), sd };
}

/** Deterministic PRNG (mulberry32) so a grading sheet can be rebuilt from its seed. */
export function rng(seed) {
	let a = seed >>> 0;
	return () => {
		a = (a + 0x6d2b79f5) >>> 0;
		let t = a;
		t = Math.imul(t ^ (t >>> 15), t | 1);
		t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	};
}

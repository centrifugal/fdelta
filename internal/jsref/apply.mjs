// Applies each delta with the JavaScript decoder and reports the failures.
// Reads {name, origin, target, delta} triples as hex-encoded JSON on stdin.
import { applyDelta } from "./fossil-delta.mjs";
import { readFileSync } from "fs";

const hex = (s) => Uint8Array.from(Buffer.from(s, "hex"));
const equal = (a, b) => a.length === b.length && a.every((v, i) => v === b[i]);

const cases = JSON.parse(readFileSync(0, "utf8"));
const failures = [];

for (const c of cases) {
  try {
    const out = Uint8Array.from(applyDelta(hex(c.origin), hex(c.delta)));
    if (!equal(out, hex(c.target))) {
      failures.push(`${c.name}: produced ${out.length} bytes, want ${c.target.length / 2}`);
    }
  } catch (e) {
    failures.push(`${c.name}: threw ${e.message}`);
  }
}

console.log(JSON.stringify({ total: cases.length, failures }));
process.exit(failures.length === 0 ? 0 : 1);

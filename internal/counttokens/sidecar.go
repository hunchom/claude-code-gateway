package counttokens

// Sidecar source written into the tokenizer working directory on first use. The
// process speaks newline-delimited JSON: one request object per line in, one
// reply object per line out, correlated by an "id" field.
//
// Each request may carry its own "model"; the sidecar resolves it against the
// installed ai-tokenizer model table (exact key, then a normalized match that
// tolerates vendor/region prefixes, dates, and version suffixes), falling back
// to COUNT_TOKENS_MODEL. Tokenizers are cached per encoding.

const sidecarPkg = `{
  "name": "ccgate-tokenizer-sidecar",
  "private": true,
  "type": "module",
  "version": "1.0.0",
  "dependencies": { "ai-tokenizer": "^1.0.6" }
}
`

const sidecarJS = `import readline from "node:readline";
import Tokenizer, { models } from "ai-tokenizer";
import { count } from "ai-tokenizer/sdk";
import * as encodings from "ai-tokenizer/encoding";

const out = (o) => process.stdout.write(JSON.stringify(o) + "\n");
const FALLBACK = "anthropic/claude-sonnet-4.5";
const DEFAULT_KEY = process.env.COUNT_TOKENS_MODEL || FALLBACK;

// Reduce a model identifier to a comparable token. Requested ids may carry
// vendor/region prefixes (us.anthropic.), bedrock version suffixes (-v1:0), and
// date stamps (-20251001); ai-tokenizer keys do not.
function normalize(s) {
  if (!s) return "";
  s = String(s).toLowerCase().trim();
  const i = s.indexOf("claude");
  if (i >= 0) s = s.slice(i);
  s = s.replace(/-v\d+:\d+$/, "");   // bedrock version, e.g. -v1:0
  s = s.replace(/[-_]\d{6,}$/, "");  // trailing date, e.g. -20251001
  s = s.replace(/[^a-z0-9]+/g, "");  // compact separators/punctuation
  return s;
}

// normalized model token -> canonical ai-tokenizer key
const index = {};
for (const key of Object.keys(models)) {
  const n = normalize(key);
  if (n && !(n in index)) index[n] = key;
}

function resolveKey(requested) {
  if (requested && models[requested]) return requested;
  const n = normalize(requested);
  if (n && index[n]) return index[n];
  if (models[DEFAULT_KEY]) return DEFAULT_KEY;
  return FALLBACK;
}

const tokCache = {};
function tokenizerFor(meta) {
  const enc = encodings[meta.encoding];
  if (!enc) throw new Error("encoding not found: " + meta.encoding);
  return tokCache[meta.encoding] || (tokCache[meta.encoding] = new Tokenizer(enc));
}

let defaultMeta;
try {
  defaultMeta = models[DEFAULT_KEY] || models[FALLBACK];
  if (!defaultMeta) throw new Error("default model not in ai-tokenizer table: " + DEFAULT_KEY);
  tokenizerFor(defaultMeta); // validate its encoding is available
  out({ ready: true, default: DEFAULT_KEY, models: Object.keys(models).length });
} catch (e) {
  out({ ready: false, error: String(e && e.stack ? e.stack : e) });
  process.exit(1);
}

const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
rl.on("line", (line) => {
  line = line.trim();
  if (!line) return;
  let id = null;
  try {
    const req = JSON.parse(line);
    id = req.id;
    const messages = Array.isArray(req.messages) ? req.messages : [];
    const tools = Array.isArray(req.tools) ? req.tools : [];
    const extra = Number.isFinite(req.extraTokens) ? req.extraTokens : 0;
    const meta = models[resolveKey(req.model)] || defaultMeta;
    const tokenizer = tokenizerFor(meta);
    const r = count({ tokenizer, model: meta, messages, tools });
    const total = (r && Number.isFinite(r.total) ? r.total : 0) + extra;
    out({ id, total });
  } catch (e) {
    out({ id, error: String(e && e.message ? e.message : e) });
  }
});
rl.on("close", () => process.exit(0));
`

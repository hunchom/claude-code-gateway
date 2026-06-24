package counttokens

// Sidecar source written into the tokenizer working directory on first use. The
// process speaks newline-delimited JSON: one request object per line in, one
// reply object per line out, correlated by an "id" field.

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
const MODEL_KEY = process.env.COUNT_TOKENS_MODEL || FALLBACK;

let tokenizer, modelMeta;
try {
  modelMeta = models[MODEL_KEY] || models[FALLBACK];
  if (!modelMeta) throw new Error("model not in ai-tokenizer table: " + MODEL_KEY);
  const enc = encodings[modelMeta.encoding];
  if (!enc) throw new Error("encoding not found: " + modelMeta.encoding);
  tokenizer = new Tokenizer(enc);
  out({ ready: true, model: MODEL_KEY, encoding: modelMeta.encoding });
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
    const r = count({ tokenizer, model: modelMeta, messages, tools });
    const total = (r && Number.isFinite(r.total) ? r.total : 0) + extra;
    out({ id, total });
  } catch (e) {
    out({ id, error: String(e && e.message ? e.message : e) });
  }
});
rl.on("close", () => process.exit(0));
`

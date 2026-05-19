// shim.ts — thin TypeScript bridge: pi extension loader ↔ coms-go binary.
// ≤ 200 LOC. No business logic — pure IPC forwarding.
//
// Binary lookup: <plugin-dir>/bin/coms-go-<goos>-<goarch>
//   goos  = process.platform mapped linux→linux, darwin→darwin, win32→windows
//   goarch = process.arch mapped x64→amd64, arm64→arm64
// Falls back to bin/coms-go (unversioned) then errors with a clear message.
//
// IPC (JSON-line over stdin/stdout):
//   → { kind:"tool_request", id, tool, params }
//   → { kind:"command",      id, name, args }
//   → { kind:"lifecycle",    event, data }
//   → { kind:"shutdown" }
//   ← { kind:"tool_response", id, ok, content, details? }
//   ← { kind:"tool_error",    id, message }
//   ← { kind:"event",         name, data }

import type { ExtensionAPI } from "@mariozechner/pi-coding-agent";
import { Type } from "@sinclair/typebox";
import { spawn, type ChildProcess } from "node:child_process";
import * as path from "node:path";
import * as fs from "node:fs";
import * as readline from "node:readline";
import * as crypto from "node:crypto";

const PLUGIN_DIR = path.dirname(new URL(import.meta.url).pathname);
const goos = ({ linux: "linux", darwin: "darwin", win32: "windows" } as any)[process.platform] ?? process.platform;
const goarch = ({ x64: "amd64", arm64: "arm64" } as any)[process.arch] ?? process.arch;

function resolveBinary(): string | null {
	const p = path.join(PLUGIN_DIR, "bin", `coms-go-${goos}-${goarch}`);
	if (fs.existsSync(p)) return p;
	const fb = path.join(PLUGIN_DIR, "bin", "coms-go");
	if (fs.existsSync(fb)) return fb;
	return null;
}

type Pending = { resolve: (v: any) => void; reject: (e: Error) => void };

function makeIpc(child: ChildProcess) {
	const pending = new Map<string, Pending>();
	const rl = readline.createInterface({ input: child.stdout!, crlfDelay: Infinity });
	rl.on("line", (raw) => {
		let msg: any;
		try { msg = JSON.parse(raw); } catch { return; }
		if (msg.kind === "tool_response" || msg.kind === "tool_error") {
			const p = pending.get(msg.id);
			if (!p) return;
			pending.delete(msg.id);
			msg.kind === "tool_error" ? p.reject(new Error(msg.message ?? "ipc error")) : p.resolve(msg);
		}
	});
	const send = (line: object) => { try { child.stdin!.write(JSON.stringify(line) + "\n"); } catch { /* child dead */ } };
	const call = (tool: string, params: object): Promise<any> => {
		const id = crypto.randomBytes(8).toString("hex");
		return new Promise((resolve, reject) => {
			pending.set(id, { resolve, reject });
			send({ kind: "tool_request", id, tool, params });
		});
	};
	return { call, send };
}

export default function (pi: ExtensionAPI) {
	// ━━ Flags ━━
	pi.registerFlag("name",       { description: "Override agent name (otherwise from frontmatter or auto-generated)", type: "string",  default: undefined });
	pi.registerFlag("purpose",    { description: "Override agent purpose (otherwise from frontmatter description)",    type: "string",  default: undefined });
	pi.registerFlag("project",    { description: "Project namespace for peer discovery",                                type: "string",  default: "default" });
	pi.registerFlag("color",      { description: "Hex color #RRGGBB (otherwise from frontmatter or palette fallback)", type: "string",  default: undefined });
	pi.registerFlag("explicit",   { description: "Hide this agent from auto-discovery; only addressable by exact name",type: "boolean", default: false });
	pi.registerFlag("server-url", { description: "coms-net server base URL (overrides env and local server.json)",     type: "string",  default: undefined });
	pi.registerFlag("auth-token", { description: "Bearer token for the coms-net hub. NEVER logged.",                   type: "string",  default: undefined });

	let localIpc: ReturnType<typeof makeIpc> | null = null;
	let netIpc:   ReturnType<typeof makeIpc> | null = null;
	let localChild: ChildProcess | null = null;
	let netChild:   ChildProcess | null = null;

	// Forward a tool call to the given child; lazily captures ipc via thunk.
	const fwd = (get: () => ReturnType<typeof makeIpc> | null, tool: string) =>
		async (_id: string, params: object) => {
			const ipc = get();
			if (!ipc) throw new Error(`coms-go: child not started (binary missing or session_start failed)`);
			const msg = await ipc.call(tool, params);
			return { content: msg.content ?? [], details: msg.details };
		};

	// ━━ Local tools (client-local) ━━
	pi.registerTool({ name: "coms_list",  label: "Coms List",
		description: "List peer agents discoverable via coms. Returns names, models, and live context-window usage. Use project=\"*\" to scan all projects. include_explicit=true reveals agents marked --explicit.",
		parameters: Type.Object({ project: Type.Optional(Type.String({ description: "Project name, or \"*\" for all. Defaults to caller's project." })), include_explicit: Type.Optional(Type.Boolean({ description: "Include --explicit agents. Default false." })) }),
		execute: fwd(() => localIpc, "coms_list") as any });

	pi.registerTool({ name: "coms_send",  label: "Coms Send",
		description: "Send a prompt to a peer agent. Returns a msg_id once the receiver acks. Use coms_get (non-blocking) or coms_await (blocking) to retrieve the response. Throws if the receiver is unreachable.",
		parameters: Type.Object({ target: Type.String({ description: "Peer name (scoped to project) or session_id." }), prompt: Type.String({ description: "The prompt to send." }), conversation_id: Type.Optional(Type.String()), response_schema: Type.Optional(Type.Any({ description: "Optional JSON Schema for the expected response shape." })) }),
		execute: fwd(() => localIpc, "coms_send") as any });

	pi.registerTool({ name: "coms_get",   label: "Coms Get",
		description: "Non-blocking poll of a pending coms_send reply. Returns status pending|complete|error and (when complete) the response.",
		parameters: Type.Object({ msg_id: Type.String({ description: "msg_id returned by coms_send." }) }),
		execute: fwd(() => localIpc, "coms_get") as any });

	pi.registerTool({ name: "coms_await", label: "Coms Await",
		description: "Block until a pending coms_send reply lands or the timeout fires. Default timeout 30 minutes (PI_COMS_TIMEOUT_MS).",
		parameters: Type.Object({ msg_id: Type.String({ description: "msg_id returned by coms_send." }), timeout_ms: Type.Optional(Type.Number({ description: "Override default timeout (ms)." })) }),
		execute: fwd(() => localIpc, "coms_await") as any });

	// ━━ Net tools (client-net) ━━
	pi.registerTool({ name: "coms_net_list",  label: "Coms Net List",
		description: "List peer agents on the coms-net hub for the current project. Returns names, models, and live context-window usage. Set include_explicit=true to reveal agents launched with --explicit.",
		parameters: Type.Object({ project: Type.Optional(Type.String({ description: "Project name (defaults to caller's project)." })), include_explicit: Type.Optional(Type.Boolean({ description: "Include --explicit agents. Default false." })) }),
		execute: fwd(() => netIpc, "coms_net_list") as any });

	pi.registerTool({ name: "coms_net_send",  label: "Coms Net Send",
		description: "INITIATE a new outbound message to a peer agent on the coms-net hub. Returns a msg_id once the server queues the prompt. Use coms_net_get or coms_net_await to retrieve the peer's reply. Do NOT use to reply to inbound messages — just answer normally and the extension auto-submits.",
		parameters: Type.Object({ target: Type.String({ description: "Peer name (scoped to project) or session_id." }), prompt: Type.String({ description: "The prompt to send." }), conversation_id: Type.Optional(Type.String()), response_schema: Type.Optional(Type.Any({ description: "Optional JSON Schema for the expected response shape." })) }),
		execute: fwd(() => netIpc, "coms_net_send") as any });

	pi.registerTool({ name: "coms_net_get",   label: "Coms Net Get",
		description: "Non-blocking poll of a reply to YOUR OWN coms_net_send. Returns status pending|complete|error and (when complete) the response.",
		parameters: Type.Object({ msg_id: Type.String({ description: "msg_id returned by coms_net_send." }) }),
		execute: fwd(() => netIpc, "coms_net_get") as any });

	pi.registerTool({ name: "coms_net_await", label: "Coms Net Await",
		description: "Block until the reply to YOUR OWN coms_net_send arrives, or the timeout fires (default 30 min). Only use msg_ids from coms_net_send, not from inbound messages.",
		parameters: Type.Object({ msg_id: Type.String({ description: "msg_id returned by coms_net_send." }), timeout_ms: Type.Optional(Type.Number({ description: "Override default timeout (ms). Server cap applies." })) }),
		execute: fwd(() => netIpc, "coms_net_await") as any });

	// ━━ Slash commands ━━
	const rng = () => crypto.randomBytes(4).toString("hex");
	pi.registerCommand("coms",     { description: "Force-refresh the coms pool widget (--all / --project <name>)",     handler: async (args) => { localIpc?.send({ kind: "command", id: rng(), name: "coms",     args: args ?? "" }); } });
	pi.registerCommand("coms-net", { description: "Force-refresh the coms-net pool widget (--all / --project <name>)", handler: async (args) => { netIpc?.send({ kind: "command", id: rng(), name: "coms-net", args: args ?? "" }); } });

	// ━━ Lifecycle ━━
	pi.on("session_start", async (_event, ctx) => {
		const binary = resolveBinary();
		if (!binary) {
			const expected = path.join(PLUGIN_DIR, "bin", `coms-go-${goos}-${goarch}`);
			ctx.ui?.notify?.(`coms-go binary not found at ${expected}; run \`just build-coms-go\` to compile.`, "error");
			return;
		}
		const env: NodeJS.ProcessEnv = {
			...process.env,
			PI_COMS_NAME:              (pi.getFlag("name")       as string  | undefined) ?? "",
			PI_COMS_PURPOSE:           (pi.getFlag("purpose")    as string  | undefined) ?? "",
			PI_COMS_PROJECT:           (pi.getFlag("project")    as string  | undefined) ?? "default",
			PI_COMS_COLOR:             (pi.getFlag("color")      as string  | undefined) ?? "",
			PI_COMS_EXPLICIT:          (pi.getFlag("explicit")   as boolean | undefined) ? "1" : "0",
			PI_COMS_NET_SERVER_URL:    (pi.getFlag("server-url") as string  | undefined) ?? process.env.PI_COMS_NET_SERVER_URL ?? "",
			PI_COMS_NET_AUTH_TOKEN:    (pi.getFlag("auth-token") as string  | undefined) ?? process.env.PI_COMS_NET_AUTH_TOKEN ?? "",
			PI_SESSION_ID: ctx.sessionId ?? "",
			PI_CWD:        ctx.cwd ?? process.cwd(),
			PI_MODEL:      ctx.model?.id ?? "",
		};
		localChild = spawn(binary, ["client-local"], { stdio: ["pipe", "pipe", "inherit"], env });
		netChild   = spawn(binary, ["client-net"],   { stdio: ["pipe", "pipe", "inherit"], env });
		localIpc = makeIpc(localChild);
		netIpc   = makeIpc(netChild);
	});

	pi.on("agent_end", async (_event, ctx) => {
		const data = { cwd: ctx.cwd ?? process.cwd(), model: ctx.model?.id ?? "" };
		localIpc?.send({ kind: "lifecycle", event: "agent_end", data });
		netIpc?.send({ kind: "lifecycle", event: "agent_end", data });
	});

	pi.on("session_shutdown", async () => {
		const TIMEOUT_MS = 3_000;
		function down(child: ChildProcess | null, ipc: ReturnType<typeof makeIpc> | null): Promise<void> {
			if (!child || !ipc) return Promise.resolve();
			ipc.send({ kind: "shutdown" });
			return new Promise((resolve) => {
				const t = setTimeout(() => { try { child.kill("SIGTERM"); } catch { /* ignore */ } resolve(); }, TIMEOUT_MS);
				child.once("exit", () => { clearTimeout(t); resolve(); });
			});
		}
		await Promise.all([down(localChild, localIpc), down(netChild, netIpc)]);
		localIpc = netIpc = localChild = netChild = null;
	});
}

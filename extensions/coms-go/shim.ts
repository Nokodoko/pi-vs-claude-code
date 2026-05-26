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

// InboundEntry — one queued inbound_prompt event from coms-go netclient.
// Per-IPC-child FIFO; drained atomically by the before_agent_start hook.
type InboundEntry = { msg_id: string; sender_name: string; sender_session: string; body: string; hops: number };

function makeIpc(child: ChildProcess) {
	const pending = new Map<string, Pending>();
	const inboundQueue: InboundEntry[] = []; // T2: FIFO per child (drained in T3)
	const rl = readline.createInterface({ input: child.stdout!, crlfDelay: Infinity });
	rl.on("line", (raw) => {
		let msg: any;
		try { msg = JSON.parse(raw); } catch { return; }
		if (msg.kind === "tool_response" || msg.kind === "tool_error") {
			const p = pending.get(msg.id);
			if (!p) return;
			pending.delete(msg.id);
			msg.kind === "tool_error" ? p.reject(new Error(msg.message ?? "ipc error")) : p.resolve(msg);
			return;
		}
		// T2: handle unsolicited event frames from the Go child. Currently the
		// only event is "inbound_prompt"; appended to a FIFO so multiple senders
		// arriving within one agent_start window are all preserved (no last-wins).
		if (msg.kind === "event" && msg.name === "inbound_prompt" && msg.data) {
			const d = msg.data;
			inboundQueue.push({
				msg_id:         String(d.msg_id ?? ""),
				sender_name:    String(d.sender_name ?? "unknown"),
				sender_session: String(d.sender_session ?? ""),
				body:           String(d.body ?? ""),
				hops:           typeof d.hops === "number" ? d.hops : 0,
			});
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
	// drainInbound — atomically pull all queued entries (FIFO). Called by the
	// before_agent_start hook (T3). Empties the queue in-place.
	const drainInbound = (): InboundEntry[] => inboundQueue.splice(0);
	return { call, send, drainInbound };
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

	pi.registerTool({ name: "coms_ask",   label: "Coms Ask",
		description: "Atomic send+await over the local (Unix-socket) transport. One tool call returns the peer's full response. Convenience wrapper around coms_send + coms_await — the receiver model is NOT auto-prompted (local transport asymmetry); use coms_net_ask for full auto-injection.",
		parameters: Type.Object({ target: Type.String({ description: "Peer name (scoped to project) or session_id." }), prompt: Type.String({ description: "The prompt to send. The peer will receive this and reply." }), timeout_ms: Type.Optional(Type.Number({ description: "Max ms to wait for reply. Default PI_COMS_TIMEOUT_MS (1 800 000)." })), conversation_id: Type.Optional(Type.String()), response_schema: Type.Optional(Type.Any({ description: "Optional JSON Schema for the expected response shape." })) }),
		execute: fwd(() => localIpc, "coms_ask") as any });

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

	pi.registerTool({ name: "coms_net_ask", label: "Coms Net Ask",
		description: "Atomic send+await over the coms-net hub. One tool call returns the peer's full response. With a target: unicast. Without a target (omit): broadcast to all peers in the project, returning a bag of responses received within timeout_ms (zero responses is NOT an error). Receiver-side auto-injection makes this fully hands-free.",
		parameters: Type.Object({ target: Type.Optional(Type.String({ description: "Peer name or session_id. Omit for broadcast." })), prompt: Type.String({ description: "The prompt to send." }), timeout_ms: Type.Optional(Type.Number({ description: "Deadline for collecting responses (ms). Default 30 000 (interactive latency)." })), conversation_id: Type.Optional(Type.String()), response_schema: Type.Optional(Type.Any()) }),
		execute: fwd(() => netIpc, "coms_net_ask") as any });

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

	// T3: drain the netIpc inboundQueue at the start of every agent turn and
	// inject a single delimited message listing every pending inbound prompt.
	// FIFO order is preserved (oldest first). No-op when the queue is empty.
	// See SPEC/coms_auto_await §5.3 / §5.4.
	pi.on("before_agent_start", async (_event, _ctx) => {
		const entries = netIpc?.drainInbound?.() ?? [];
		if (entries.length === 0) return {};
		const n = entries.length;
		const numbered = entries
			.map((inj, i) => `[${i + 1}/${n}] From ${inj.sender_name} (msg ${inj.msg_id}):\n${inj.body}`)
			.join("\n\n");
		const text =
			`You have ${n} pending message${n === 1 ? "" : "s"}:\n\n` +
			numbered +
			`\n\nAddress each pending message in your reply. Your full response will be returned to each sender automatically.`;
		const senders = entries.map(e => e.sender_name).join(", ");
		return {
			message: {
				customType: "coms_inbound",
				content: [{ type: "text", text }],
				display: `coms-net: ${n} inbound message${n === 1 ? "" : "s"} from ${senders}`,
				details: {
					entries: entries.map(e => ({
						msg_id:         e.msg_id,
						sender_name:    e.sender_name,
						sender_session: e.sender_session,
						hops:           e.hops,
					})),
				},
			},
		};
	});

	pi.on("agent_end", async (event, ctx) => {
		const baseData = { cwd: ctx.cwd ?? process.cwd(), model: ctx.model?.id ?? "" };
		// T1.5: plumb the last assistant text through the net lifecycle frame so
		// handleLifecycle (netclient/client.go) can call onAgentEnd and POST the
		// reply back. Local payload stays unchanged — the local transport has no
		// onAgentEnd path. See SPEC/coms_auto_await §11.1.
		const messages = (event as any)?.messages ?? [];
		const lastMsg  = [...messages].reverse().find((m: any) => m?.role === "assistant");
		const lastText = (lastMsg?.content ?? [])
			.filter((b: any) => b?.type === "text")
			.map((b: any) => (typeof b?.text === "string" ? b.text : ""))
			.join("");
		localIpc?.send({ kind: "lifecycle", event: "agent_end", data: baseData });
		netIpc?.send({   kind: "lifecycle", event: "agent_end", data: { ...baseData, last_text: lastText } });
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

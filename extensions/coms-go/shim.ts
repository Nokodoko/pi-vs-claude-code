// shim.ts — thin TypeScript bridge between the pi extension loader and the
// coms-go binary. This file is the only TypeScript in extensions/coms-go/.
//
// FULL IMPLEMENTATION IS PART OF T6. This is the structural skeleton only.
//
// Responsibilities (T6 will fill in each section):
//   1. Register identity flags: --name, --purpose, --project, --color,
//      --explicit, --server-url, --auth-token (mirrors coms.ts + coms-net.ts).
//   2. On session_start: spawn `coms-go client-local` and `coms-go client-net`
//      as long-lived child processes; pipe JSON-line IPC over stdin/stdout.
//   3. Register eight tools (coms_list/send/get/await + coms_net_list/send/get/await)
//      as thin wrappers that send a JSON-line IPC request and return the reply.
//   4. Register /coms and /coms-net slash commands as IPC passthroughs.
//   5. On session_shutdown / agent_end: send "shutdown" IPC line and wait briefly.

import type { ExtensionAPI } from "@mariozechner/pi-coding-agent";

export default function (_pi: ExtensionAPI) {
  // T6: implement flags, lifecycle hooks, tool registrations, and IPC wiring.
}

set dotenv-load := true

default:
    @just --list

# prime

# Launch Claude Code and run /prime
primecc:
    claude --dangerously-skip-permissions --model "opus[1m]" "/prime"

# Launch Pi and run /prime
primepi:
    pi "/prime"

# g1

# 1. default pi
pi:
    pi

# 2. Pure focus pi: strip footer and status line entirely
ext-pure-focus:
    pi -e extensions/pure-focus.ts

# 3. Minimal pi: model name + 10-block context meter
ext-minimal:
    pi -e extensions/minimal.ts -e extensions/theme-cycler.ts

# 4. Cross-agent pi: load commands from .claude/, .gemini/, .codex/ dirs
ext-cross-agent:
    pi -e extensions/cross-agent.ts -e extensions/minimal.ts

# 5. Purpose gate pi: declare intent before working, persistent widget, focus the system prompt on the ONE PURPOSE for this agent
ext-purpose-gate:
    pi -e extensions/purpose-gate.ts -e extensions/minimal.ts

# 6. Customized footer pi: Tool counter, model, branch, cwd, cost, etc.
ext-tool-counter:
    pi -e extensions/tool-counter.ts

# 7. Tool counter widget: tool call counts in a below-editor widget
ext-tool-counter-widget:
    pi -e extensions/tool-counter-widget.ts -e extensions/minimal.ts

# 8. Subagent widget: /sub <task> with live streaming progress
ext-subagent-widget:
    pi -e extensions/subagent-widget.ts -e extensions/pure-focus.ts -e extensions/theme-cycler.ts

# 9. TillDone: task-driven discipline — define tasks before working
ext-tilldone:
    pi -e extensions/tilldone.ts -e extensions/theme-cycler.ts

#g2

# 10. Agent team: dispatcher orchestrator with team select and grid dashboard
ext-agent-team:
    pi -e extensions/agent-team.ts -e extensions/theme-cycler.ts

# 11. System select: /system to pick an agent persona as system prompt
ext-system-select:
    pi -e extensions/system-select.ts -e extensions/minimal.ts -e extensions/theme-cycler.ts

# 12. Launch with Damage-Control safety auditing
ext-damage-control:
    pi -e extensions/damage-control.ts -e extensions/minimal.ts -e extensions/theme-cycler.ts

# 13. Agent chain: sequential pipeline orchestrator
ext-agent-chain:
    pi -e extensions/agent-chain.ts -e extensions/theme-cycler.ts

#g3

# 14. Pi Pi: meta-agent that builds Pi agents with parallel expert research
ext-pi-pi:
    pi -e extensions/pi-pi.ts -e extensions/theme-cycler.ts

# 17. Coms: peer-to-peer messaging between Pi agents on the same machine
# Pass any pi/extension flags through, e.g.: just ext-coms --name dev --color "#72F1B8"
ext-coms *args:
    pi -e extensions/coms.ts -e extensions/minimal.ts -e extensions/theme-cycler.ts {{args}}

# coms demo

# Coms — planner agent (cyan). Extra args append, e.g.: just ext-coms-planner --explicit
ext-coms-planner *args:
    pi -e extensions/coms.ts -e extensions/minimal.ts -e extensions/theme-cycler.ts --name planner --purpose "Plans the work, audio-first" --color "#36F9F6" {{args}}

# Coms — coder agent (pink). Extra args append.
ext-coms-coder *args:
    pi -e extensions/coms.ts -e extensions/minimal.ts -e extensions/theme-cycler.ts --name coder --purpose "Writes and edits code" --color "#FF7EDB" {{args}}

# Coms — open planner + coder in two terminals
ext-coms-pair:
    #!/usr/bin/env bash
    osascript -e "tell application \"Terminal\" to do script \"cd '{{justfile_directory()}}' && just ext-coms-planner\""
    osascript -e "tell application \"Terminal\" to do script \"cd '{{justfile_directory()}}' && just ext-coms-coder\""

# Coms — spawn 4 coders in parallel terminals
ext-coms-team-4:
    #!/usr/bin/env bash
    declare -a names=("coder-1" "coder-2" "coder-3" "coder-4")
    declare -a colors=("#72F1B8" "#36F9F6" "#FF7EDB" "#FEDE5D")

    for i in {0..3}; do
        osascript -e "tell application \"Terminal\" to do script \"cd '{{justfile_directory()}}' && source .env && pi -e extensions/coms.ts -e extensions/minimal.ts -e extensions/theme-cycler.ts --name '${names[$i]}' --purpose 'Writes and edits code' --color '${colors[$i]}'\""
    done

# coms-net (HTTP/SSE hub)

# Start a local coms-net server (binds 127.0.0.1, OS-claimed port)
coms-net-server:
    bun scripts/coms-net-server.ts

# Start a LAN-visible coms-net server (binds 0.0.0.0, requires PI_COMS_NET_AUTH_TOKEN)
coms-net-server-lan:
    PI_COMS_NET_HOST=0.0.0.0 bun scripts/coms-net-server.ts

# Pi with networked coms client (auto-discovers local server.json)
# Pass any flags through, e.g.: just ext-coms-net --name dev --server-url http://… --auth-token …
ext-coms-net *args:
    pi -e extensions/coms-net.ts -e extensions/minimal.ts -e extensions/theme-cycler.ts {{args}}

# Alias: just coms = just ext-coms-net
alias coms := ext-coms-net

#ext

# 15. Session Replay: scrollable timeline overlay of session history (legit)
ext-session-replay:
    pi -e extensions/session-replay.ts -e extensions/minimal.ts

# 16. Theme cycler: Ctrl+X forward, Ctrl+Q backward, /theme picker
ext-theme-cycler:
    pi -e extensions/theme-cycler.ts -e extensions/minimal.ts

# utils

# Open pi with one or more stacked extensions in a new terminal: just open minimal tool-counter
open +exts:
    #!/usr/bin/env bash
    args=""
    for ext in {{exts}}; do
        args="$args -e extensions/$ext.ts"
    done
    cmd="cd '{{justfile_directory()}}' && pi$args"
    escaped="${cmd//\\/\\\\}"
    escaped="${escaped//\"/\\\"}"
    osascript -e "tell application \"Terminal\" to do script \"$escaped\""

# Open every extension in its own terminal window
all:
    just open pi
    just open pure-focus 
    just open minimal theme-cycler
    just open cross-agent minimal
    just open purpose-gate minimal
    just open tool-counter
    just open tool-counter-widget minimal
    just open subagent-widget pure-focus theme-cycler
    just open tilldone theme-cycler
    just open agent-team theme-cycler
    just open system-select minimal theme-cycler
    just open damage-control minimal theme-cycler
    just open agent-chain theme-cycler
    just open pi-pi theme-cycler
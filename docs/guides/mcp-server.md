# MCP Server: Share Memory With External Agents

`picoclaw mcp serve` exposes a workspace over MCP so other agents can read
picoclaw's persona, memory cards and session archive, and write back their own
transcripts and distilled memories.

It is a **standalone process**: it shares files with the gateway but never
joins it, and it **never calls a model**. External agents bring their own
tokens; picoclaw is only the memory brain.

## Configure

Create `<workspace>/mcp/config.json` (default workspace:
`$PICOCLAW_HOME/workspace`):

```json
{
  "host": "127.0.0.1",
  "port": 18901,
  "agents": [
    {
      "name": "claude-code",
      "token": "change-me-long-random",
      "read_main_session": true,
      "write_memory": true,
      "write_mood": false
    }
  ]
}
```

- `name` becomes the caller's identity: it is recorded as the source of every
  memory card and as the owner of archived sessions. Letters, digits, `.`,
  `_`, `-` only.
- `token` is presented as `Authorization: Bearer <token>`.
- `read_main_session` (default `true`): allow reading the owner's own session
  transcripts.
- `write_memory` (default `true`): allow `remember`.
- `write_mood` (default `false`): allow `mood_add`.

Keep the file at mode `0600`. Bind to loopback or a Tailscale/LAN address —
this endpoint exposes private memories, do not put it on the public internet.

## Run

```sh
picoclaw mcp serve                 # uses <workspace>/mcp/config.json
picoclaw mcp serve --host 100.x.y.z --port 18901
picoclaw mcp serve --dir /path/to/workspace
```

The endpoint is `POST http://<host>:<port>/mcp` (streamable HTTP, JSON
responses). `GET /healthz` returns `ok` without authentication.

Client-side MCP configuration looks like:

```json
{
  "mcpServers": {
    "picoclaw": {
      "type": "http",
      "url": "http://100.x.y.z:18901/mcp",
      "headers": { "Authorization": "Bearer change-me-long-random" }
    }
  }
}
```

## Tools

| Tool | Purpose |
| --- | --- |
| `profile_get` | Persona files (`AGENT.md`, `MEMORY.md`) and the mood line |
| `memory_context` | Recalled memory rendered as a ready-to-inject block |
| `memory_search` | Matching cards with text, kind, key, tags and source |
| `mood_show` | Current mood computed from mood events |
| `session_read` | Read an owner session, or `ext/<id>` for your own archive |
| `session_begin` | Open an external session, get persona + memory context |
| `session_append` | Append verbatim messages to the transcript |
| `session_end` | Close the session and store its summary |
| `remember` | Store a distilled memory card (source = your agent name) |
| `mood_add` | Record a mood event (needs `write_mood`) |

## Typical Call Flow

1. `session_begin({topic})` → returns `session_id` plus persona and recalled
   memory. Put that context into your own prompt.
2. Do the work with your own model and tools.
3. `session_append({session_id, messages})` after each step, or once at the
   end. Messages are stored verbatim (`role`, `content`, optional `name`,
   `ts`).
4. `remember({text, key, kind})` for anything worth keeping long term. Use
   `key` to supersede an older card on the same subject.
5. `session_end({session_id, summary})` to close the archive.

## Storage Layout

```
<workspace>/
  mcp/config.json                  # agents and listen address
  sessions-external/<agent>/<id>/  # your archived transcripts
    meta.json                      # status, topic, summary, counters
    transcript.jsonl               # verbatim messages
  fish/cards.jsonl                 # shared memory cards (src = agent name)
  fish/mood/events.jsonl           # shared mood events
  sessions/                        # owner sessions (read-only via MCP)
```

External transcripts stay separate from owner sessions, so they never pollute
the owner's own session list. Memory cards and mood events are shared on
purpose: that is the point of the integration. `cards.jsonl` updates are
serialized with a lock file, so concurrent writers (gateway, `picoclaw fish`,
this server) cannot lose each other's cards.

## Trust Notes

Any agent holding a token can read memories and write new ones. Issue tokens
sparingly, keep them out of logs, and use the recorded `src` to audit or clean
up what a given agent wrote.

# mcp

vmlab speaks Model Context Protocol over stdio. Launch it from any agent host:

```sh
vmlab serve --mcp                # read-only tools
vmlab serve --mcp --allow-write  # adds run/web/gui write tools
```

## Wire format

JSON-RPC 2.0, newline-delimited. Subset:

- `initialize`
- `ping`
- `tools/list`
- `tools/call`

## Tools

### Read-only (always available)

| Tool | Args |
|---|---|
| `vmlab_targets` | _none_ |
| `vmlab_doctor` | `selector?: string` |
| `vmlab_evidence` | `limit?: number` |

### Write (requires `--allow-write`)

| Tool | Args |
|---|---|
| `vmlab_run` | `selector: string`, one of `command: string` / `flowPath: string`, `maxParallel?: number`, `failFast?: boolean` |
| `vmlab_web` | `target: string`, `args: string[]` |
| `vmlab_gui` | `target: string`, `kind: "click"\|"type"\|"screenshot"\|"run"`, `selector?`, `text?`, `path?` |

## Claude Code

```jsonc
// ~/.claude/settings.json
{
  "mcpServers": {
    "vmlab": {
      "command": "vmlab",
      "args": ["serve", "--mcp", "--allow-write"]
    }
  }
}
```

Once registered, Claude Code can drive a 3-target fan-out flow end-to-end with
no Bash glue: `vmlab_doctor` to confirm health, `vmlab_run` with a flow path,
then `vmlab_evidence` to find the run-id for inspection.

## Output shape

Each tool returns a single `text` content block with stringified JSON. Errors
return `{"isError": true, "content": [{"type":"text","text":"<err>"}]}`.

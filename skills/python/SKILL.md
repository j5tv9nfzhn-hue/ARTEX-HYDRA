---
name: python
description: Run Python through Bash in the SDK sandbox. Captured HTTP traffic is read back with the traffic_search/traffic_get/traffic_blob tools; replay by rebuilding the request by hand, there is no replay tool.
---

# Python In The Sandbox

Use `Bash` for Python. There is no separate Python executor.

Prefer writing reusable scripts to a `.py` file and running them with
`python3 <name>.py`. For short one-off transformations, `python3 -c` or a
small here-document is fine.

Do not try to pick an interpreter through a tool parameter — there is none.
Put the interpreter invocation in the command itself: `python3 -c '...'`
`shell=python3, cmd="..."`. The `shell=<interpreter>` shortcut breaks
in subtle ways — `python3` works only with `login=False` (because the
SDK adds `-l`/`-i`), and other interpreters (`node`, `ruby`, `perl`)
take `-e` not `-c` so they fail even with `login=False`.

## Working With Captured Traffic

Captured HTTP traffic is read back with this system's own tools. There is no
`caido_api` Python module in the sandbox image — importing it will fail:

- `traffic_search(host=, contains=, body_contains=, limit=, page=)` — find
  captured requests for a host. `contains` matches the request line and headers,
  `body_contains` matches the body.
- `traffic_get(id)` — fetch one request/response verbatim (truncated when huge).
- `traffic_blob(hash, offset=, length=)` — read a truncated body in 8KB chunks.

There is no replay-and-modify tool. To replay a captured request with changes,
rebuild it by hand with `curl` / `httpx` / `requests` in `Bash` (or in a script
run with `python3`), changing the URL, params, headers, body or cookies
yourself. Work from the raw request you pulled with `traffic_get` so you do not
invent fields.

For one-off arbitrary requests (e.g. probing a fresh endpoint, hitting an
external API), use `Bash` with `curl` / `httpx` / `requests`. Anything sent
through the recording proxy is captured and searchable with `traffic_search`
afterwards; to replay a captured request with modifications, rebuild it by hand in
`Bash` — there is no replay tool.

## Workflow

For iterative exploit work, put code in a file:

```text
1. Create or edit a task-unique script (e.g. `poc_<task-id>.py`, so it can't
   clobber a project file or another agent's script) with `apply_patch`.
2. Run it with `Bash`: `python3 poc_<task-id>.py`.
3. Edit and rerun until the proof-of-concept is reliable.
```

## Installing extra packages

The sandbox's Python lives in `/app/.venv`, and it is the active virtualenv
(`python3` / `pip` already resolve to it). The following common libraries are
**pre-installed** — import them directly, no install step needed:
`requests`, `httpx`, `beautifulsoup4` (`bs4`), `lxml`, `pyjwt` (`jwt`),
`cryptography`.

To add a one-off dependency for an exploit script, use `uv` (already in the
image and much faster than pip):

```bash
uv pip install --python /app/.venv/bin/python <package>
```

Plain `pip install <package>` also works because the venv is active. Install
before you import, so scripts don't fail with `ModuleNotFoundError`.

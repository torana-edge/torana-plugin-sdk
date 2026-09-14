#!/usr/bin/env python3
"""Generate the human and Rust-facing capability inventories from capabilities.json."""
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
catalog = json.loads((ROOT / "capabilities.json").read_text())
commands = [c for c in catalog["commands"] if c.get("implemented", False)]

md = ["# Torana plugin capabilities", "", "Generated from `capabilities.json`; run `scripts/generate-capabilities.py` after editing the catalog.", "", "## Hook grants", "", "| Permission | Hook |", "| --- | --- |"]
md += [f"| `{p}` | `{h}` |" for p, h in sorted(catalog.get("hook_grants", {}).items())]
md += ["", "## Commands", "", "| Command | Permission | Arguments | Result | Hooks | Helpers |", "| --- | --- | --- | --- | --- | --- |"]
for c in commands:
    md.append(f"| `{c['command']}` | `{c['permission']}` | `{c['arguments']}` | `{c['result']}` | {', '.join(f'`{h}`' for h in c['hooks'])} | {', '.join(f'`{h}`' for h in c['helpers'])} |")
md += ["", "## IR write permissions", ""]
md += [f"- `{p}`" for p in sorted(catalog.get("write_permissions", []))]
reference = "\n".join(md) + "\n"

rust = {
    "hooks": catalog["hooks"],
    "hook_grants": dict(sorted(catalog.get("hook_grants", {}).items())),
    "metadata_grants": sorted(catalog.get("metadata_grants", [])),
    "write_permissions": sorted(catalog.get("write_permissions", [])),
    "commands": [{k: c[k] for k in ("command", "permission", "arguments", "result", "hooks", "helpers")} for c in commands],
}
rust_json = json.dumps(rust, indent=2, sort_keys=False) + "\n"
rust_source = '''// Generated from ../../capabilities.json by scripts/generate-capabilities.py.
// The runtime uses this inventory to reject commands absent from its ABI build.
#[derive(Clone, Copy)]
pub(crate) struct CommandContract {
    pub command: &'static str,
    pub arguments: &'static str,
    pub result: &'static str,
}

pub(crate) const COMMANDS: &[CommandContract] = &[\n'''
for c in commands:
    rust_source += f'''    CommandContract {{
        command: {json.dumps(c["command"])},
        arguments: {json.dumps(c["arguments"])},
        result: {json.dumps(c["result"])},
    }},
'''
rust_source += '''];

pub(crate) fn command(name: &str) -> Option<CommandContract> {
    COMMANDS.iter().copied().find(|entry| entry.command == name)
}
'''
rust_source_path = ROOT / "rust/torana-plugin-sdk/src/capability_contract.rs"
if "--check" in sys.argv:
    for path, expected in ((ROOT / "capabilities-reference.md", reference), (ROOT / "capabilities-rust.json", rust_json), (rust_source_path, rust_source)):
        if not path.exists() or path.read_text() != expected:
            print(f"stale generated capability file: {path}", file=sys.stderr)
            raise SystemExit(1)
else:
    (ROOT / "capabilities-reference.md").write_text(reference)
    (ROOT / "capabilities-rust.json").write_text(rust_json)
    rust_source_path.write_text(rust_source)

# ABI v2 migration baseline

Migration C completed on 2026-08-05 for the Edge host, Go SDK, and all nine
official Go plugins. The merged SDK baseline is
`995c0bd40baa44098de11c137ceba9e8e79fdc41`; Edge baseline
`5727793c79f1ff1fead3c983624773cd23c931ef` and plugin baseline
`0e8a1014af138588e26ed2d1b9abfdaf65560bf6` consume it.

ABI v2 owns the ordered request model, replacement-domain validation,
provenance-aware mutation helpers, observable cache prefix, typed refusal
classification, and narrow write-permission vocabulary.

The Rust crate remains ABI v1 and is incompatible with the v2-only host. This
is an explicit release-readiness limitation, not a compatibility promise.
